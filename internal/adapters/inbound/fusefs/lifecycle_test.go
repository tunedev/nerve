package fusefs_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/inbound/fusefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

// helperDir names the directory the re-executed test binary mounts at. Set, it
// turns this binary into the process the crash case needs; unset, the binary
// runs the tests.
const helperDir = "NERVE_FUSEFS_HELPER_DIR"

// mountBudget bounds the wait for the helper's mount to appear, so a helper
// that cannot mount fails this test rather than hanging the suite.
const mountBudget = 10 * time.Second

// serveUntilKilled mounts at dir and blocks. Nothing here unmounts: what this
// process is for is dying while it holds a mount, which is what SIGKILL, a
// panic and the OOM killer all leave behind.
func serveUntilKilled(dir string) {
	mount, err := fusefs.Serve(dir, memfs.New())
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: %v\n", err)
		os.Exit(1)
	}
	mount.Wait()
}

// A process killed while it holds a mount cannot clean up after itself, so the
// mount it leaves is the kernel's until something unmounts it. This is the
// whole of the crash case: the point is occupied and answers ENOTCONN, and
// mounting there again is what a restart does.
func TestAKilledProcessLeavesNoMountBehind(t *testing.T) {
	requireFUSE(t)

	dir := t.TempDir()
	guard(t, dir)
	kill(t, spawnHelper(t, dir))

	// The premise. A mount whose server is gone is still in the table, and
	// every path through it fails with ENOTCONN rather than not existing.
	if _, err := os.Stat(dir); !errors.Is(err, syscall.ENOTCONN) {
		t.Fatalf("stat of the point the killed process mounted at gave %v, want ENOTCONN", err)
	}
	if n := mountsAt(t, dir); n != 1 {
		t.Fatalf("the killed process left %d mounts at %s, want the 1 this case is about", n, dir)
	}

	// The restart. Serving at the same point clears what is there first, so
	// this succeeds rather than mounting a second filesystem over a dead one.
	tree := memfs.New()
	served, err := fusefs.Serve(dir, tree)
	if err != nil {
		t.Fatalf("Serve over the mount the killed process left: %v", err)
	}
	t.Cleanup(func() {
		if err := served.Unmount(); err != nil {
			t.Errorf("Unmount: %v", err)
		}
		served.Wait()
	})

	if err := os.WriteFile(filepath.Join(dir, "note"), []byte("after the restart"), 0o644); err != nil {
		t.Fatalf("WriteFile through the new mount: %v", err)
	}
	stored, err := tree.Read(t.Context(), "/note")
	if err != nil {
		t.Fatalf("Read from the new tree: %v", err)
	}
	if string(stored) != "after the restart" {
		t.Errorf("the new tree holds %q, want %q", stored, "after the restart")
	}

	// One mount and not two. A mount stacked over the dead one would serve
	// this just as well and leave the corpse underneath it.
	if n := mountsAt(t, dir); n != 1 {
		t.Errorf("%d mounts at %s after the restart, want 1", n, dir)
	}
}

// The other way a mount is left behind is the process that does get to run its
// shutdown: the ordinary unmount fails while a descriptor opened through the
// mount is still open, and a caller about to exit cannot wait that out.
func TestUnmountLeavesTheTableEvenWhileSomethingHoldsIt(t *testing.T) {
	requireFUSE(t)

	dir := t.TempDir()
	served, err := fusefs.Serve(dir, memfs.New())
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	guard(t, dir)

	at := filepath.Join(dir, "note")
	if err := os.WriteFile(at, []byte("held open"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	held, err := os.Open(at)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := served.Unmount(); err != nil {
		t.Fatalf("Unmount with a descriptor still open: %v", err)
	}
	if n := mountsAt(t, dir); n != 0 {
		t.Errorf("%d mounts at %s after Unmount, want 0", n, dir)
	}

	// Lazily, not violently: the descriptor that blocked the unmount keeps
	// answering, and the mount is freed when it closes. The path it was
	// opened by does not, which is what leaving the table means.
	read, err := io.ReadAll(held)
	if err != nil {
		t.Errorf("the held descriptor stopped answering: %v", err)
	} else if string(read) != "held open" {
		t.Errorf("the held descriptor read %q, want %q", read, "held open")
	}
	if _, err := os.Stat(at); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the path still stats after the mount left the table: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	served.Wait()
}

// spawnHelper starts this binary as the process that mounts at dir, and
// returns it once the mount is up.
func spawnHelper(t *testing.T, dir string) *exec.Cmd {
	t.Helper()

	var said bytes.Buffer
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperDir+"="+dir)
	cmd.Stdout = &said
	cmd.Stderr = &said
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the helper: %v", err)
	}

	until := time.Now().Add(mountBudget)
	for mountsAt(t, dir) == 0 {
		if time.Now().After(until) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("the helper did not mount at %s within %s: %s", dir, mountBudget, said.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cmd
}

// kill ends the helper the way nothing recovers from.
func kill(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill the helper: %v", err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) {
		t.Fatalf("the helper ended with %v, want a signal", err)
	}
}

// guard clears whatever is mounted at dir when the test is over. Every case
// here unmounts what it mounted; this is what stops a case that fails halfway
// leaving a mount on the machine it ran on, which is also what would make
// t.TempDir's own cleanup fail.
func guard(t *testing.T, dir string) {
	t.Helper()

	t.Cleanup(func() {
		if mountsAt(t, dir) == 0 {
			return
		}
		if out, err := exec.Command("fusermount3", "-u", "-z", dir).CombinedOutput(); err != nil {
			t.Errorf("clearing the mount left at %s: %v: %s", dir, err, out)
		}
	})
}

// mountsAt counts the entries the mount table holds for dir. Nothing else can
// tell an unmounted point from one a dead process is still mounted at, and it
// is the count rather than the answer because a mount stacked over another
// serves exactly as well as one.
func mountsAt(t *testing.T, dir string) int {
	t.Helper()

	table, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		t.Fatalf("open the mount table: %v", err)
	}
	defer table.Close()

	count := 0
	scan := bufio.NewScanner(table)
	for scan.Scan() {
		// Field five is the mount point, with spaces and the escape
		// itself written as octal.
		fields := strings.Fields(scan.Text())
		if len(fields) < 5 {
			continue
		}
		point := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		if point == dir {
			count++
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("read the mount table: %v", err)
	}
	return count
}
