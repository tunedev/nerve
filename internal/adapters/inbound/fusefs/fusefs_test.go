package fusefs_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/inbound/fusefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Nothing in this file asserts anything about a mount without mounting one.
// A filesystem exercised through its own port is the plugin's own suite; what
// this package adds is the kernel as the caller, and only a real mount has one.

// mount serves fsys at a directory of its own and returns the host path. The
// unmount is checked rather than deferred silently: it fails with EBUSY while a
// descriptor is still open, and a test that ignored that would leave a mount on
// the machine it ran on.
func mount(t *testing.T, fsys vfs.FileSystem) string {
	t.Helper()
	requireFUSE(t)

	dir := t.TempDir()
	served, err := fusefs.Serve(dir, fsys)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() {
		if err := served.Unmount(); err != nil {
			t.Errorf("Unmount: %v", err)
		}
		served.Wait()
	})
	return dir
}

// requireFUSE skips when the host cannot mount. Mounting is a privilege: the
// device has to be there and the setuid helper has to be on PATH, and neither
// is true in every container. This is the first thing that fails on a clean
// machine, and it fails here rather than as a confusing error inside a case.
func requireFUSE(t *testing.T) {
	t.Helper()

	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skipf("no /dev/fuse on this host: %v", err)
	}
	if _, err := exec.LookPath("fusermount3"); err != nil {
		t.Skipf("no fusermount3 on this host: %v", err)
	}
}

// A tree in memory has no host behind it at all, so what a caller reads through
// the mount can only have come through the port.
func TestTheKernelSeesAnOrdinaryTree(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	if err := os.Mkdir(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "one.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	read, err := os.ReadFile(filepath.Join(dir, "notes", "one.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(read) != "hello" {
		t.Errorf("read %q through the mount, want %q", read, "hello")
	}

	// The write landed in the tree and not in the directory the mount
	// stands on, which is the whole claim of an inbound adapter.
	stored, err := tree.Read(t.Context(), "/notes/one.txt")
	if err != nil {
		t.Fatalf("Read from the tree: %v", err)
	}
	if string(stored) != "hello" {
		t.Errorf("the tree holds %q, want %q", stored, "hello")
	}

	entries, err := os.ReadDir(filepath.Join(dir, "notes"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "one.txt" {
		t.Errorf("listed %v, want one entry named one.txt", entries)
	}

	info, err := os.Stat(filepath.Join(dir, "notes", "one.txt"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 5 || info.IsDir() {
		t.Errorf("stat says size %d dir %v, want 5 and false", info.Size(), info.IsDir())
	}
}

// A write that did not go through the mount is still what the mount serves:
// nothing here holds a copy of the tree.
func TestTheTreeIsWhatTheMountServes(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	if err := tree.Write(t.Context(), "/planted", []byte("from behind")); err != nil {
		t.Fatalf("Write into the tree: %v", err)
	}

	read, err := os.ReadFile(filepath.Join(dir, "planted"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(read) != "from behind" {
		t.Errorf("read %q, want %q", read, "from behind")
	}
}

// Entries larger than one FUSE request are what prove the handle serves
// offsets rather than answering one read: the kernel splits this into many.
func TestAnEntryLargerThanOneRequest(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	content := bytes.Repeat([]byte("nerve"), 100_000)
	at := filepath.Join(dir, "big")
	if err := os.WriteFile(at, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	read, err := os.ReadFile(at)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(read, content) {
		t.Errorf("read %d bytes, want the %d written", len(read), len(content))
	}

	// A read at an offset the kernel did not choose, to make the same point
	// about a caller that seeks.
	file, err := os.Open(at)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer file.Close()

	middle := make([]byte, 5)
	if _, err := file.ReadAt(middle, 250_000); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(middle, content[250_000:250_005]) {
		t.Errorf("read %q at 250000, want %q", middle, content[250_000:250_005])
	}
}

// Overwriting a longer entry with a shorter one is where a mount that never
// truncates keeps the tail. O_TRUNC arrives as a size, not as an open flag.
func TestOverwritingCutsTheEntry(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	at := filepath.Join(dir, "note")
	if err := os.WriteFile(at, []byte("a long first line"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(at, []byte("short"), 0o644); err != nil {
		t.Fatalf("WriteFile again: %v", err)
	}

	read, err := os.ReadFile(at)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(read) != "short" {
		t.Errorf("read %q, want %q", read, "short")
	}
}

// Truncate is the verb the port reaches through an accessor, and a tree in
// memory has none: the mount reads and writes back instead, so both directions
// have to hold.
func TestTruncateCutsAndPads(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	at := filepath.Join(dir, "note")
	if err := os.WriteFile(at, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Truncate(at, 3); err != nil {
		t.Fatalf("Truncate down: %v", err)
	}
	if read, _ := os.ReadFile(at); string(read) != "abc" {
		t.Errorf("after cutting to 3, read %q, want %q", read, "abc")
	}

	if err := os.Truncate(at, 5); err != nil {
		t.Fatalf("Truncate up: %v", err)
	}
	if read, _ := os.ReadFile(at); string(read) != "abc\x00\x00" {
		t.Errorf("after padding to 5, read %q, want %q", read, "abc\x00\x00")
	}
}

// Rename moves between directories, so both ends have to be spelled in the
// tree's vocabulary and not in the host's.
func TestRenameAcrossDirectories(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	if err := os.MkdirAll(filepath.Join(dir, "from"), 0o755); err != nil {
		t.Fatalf("MkdirAll from: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "to"), 0o755); err != nil {
		t.Fatalf("MkdirAll to: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "from", "note"), []byte("moved"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "from", "note"), filepath.Join(dir, "to", "note")); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	stored, err := tree.Read(t.Context(), "/to/note")
	if err != nil {
		t.Fatalf("the tree does not hold /to/note: %v", err)
	}
	if string(stored) != "moved" {
		t.Errorf("the tree holds %q at /to/note, want %q", stored, "moved")
	}
	if _, err := os.Stat(filepath.Join(dir, "from", "note")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old name still stats: %v", err)
	}
}

// A rename leaves the moved node standing at the name it was moved to. A mount
// that remembered the path a node was looked up under would write the next
// change back to the name that is gone.
func TestAMovedEntryIsWrittenAtItsNewName(t *testing.T) {
	tree := memfs.New()
	dir := mount(t, tree)

	at := filepath.Join(dir, "before")
	if err := os.WriteFile(at, []byte("one"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Rename(at, filepath.Join(dir, "after")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "after"), []byte("two"), 0o644); err != nil {
		t.Fatalf("WriteFile after the rename: %v", err)
	}

	stored, err := tree.Read(t.Context(), "/after")
	if err != nil {
		t.Fatalf("Read /after: %v", err)
	}
	if string(stored) != "two" {
		t.Errorf("the tree holds %q at /after, want %q", stored, "two")
	}
	if _, err := tree.Stat(t.Context(), "/before"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the tree still holds /before: %v", err)
	}
}

// Deleting a directory with something in it is the one refusal a caller
// branches on, and the port spells it with the sentinel that means "exists".
func TestRemovingADirectoryWithEntriesInIt(t *testing.T) {
	dir := mount(t, memfs.New())

	if err := os.Mkdir(filepath.Join(dir, "full"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full", "note"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := os.Remove(filepath.Join(dir, "full"))
	if !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("removing a directory with an entry in it gave %v, want ENOTEMPTY", err)
	}

	// The ordinary recursive delete branches on exactly that answer.
	if err := os.RemoveAll(filepath.Join(dir, "full")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "full")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directory is still there: %v", err)
	}
}

// Chmod is the one verb in the port that sets a mode, and a mount is where a
// caller reaches it by the ordinary spelling.
func TestChmodThroughTheMount(t *testing.T) {
	dir := mount(t, memfs.New())

	at := filepath.Join(dir, "note")
	if err := os.WriteFile(at, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(at, 0o600); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	info, err := os.Stat(at)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode is %v, want 0600", info.Mode().Perm())
	}
}

// A missing name is ENOENT and not an I/O error, which is what every caller
// that checks for a file existing is reading.
func TestAMissingNameIsNotThere(t *testing.T) {
	dir := mount(t, memfs.New())

	if _, err := os.ReadFile(filepath.Join(dir, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading a name that is not there gave %v, want ErrNotExist", err)
	}
}

// Neither sort of link is served. A hard link would give one entry two paths,
// which is the second spelling of a location that workspace.Scope.Path exists
// to prevent; a symbolic link is one the port cannot describe.
func TestLinksAreRefused(t *testing.T) {
	dir := mount(t, memfs.New())

	if err := os.WriteFile(filepath.Join(dir, "note"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink("note", filepath.Join(dir, "soft")); err == nil {
		t.Error("Symlink succeeded; the mount has no verb for it")
	}
	if err := os.Link(filepath.Join(dir, "note"), filepath.Join(dir, "hard")); err == nil {
		t.Error("Link succeeded; the mount has no verb for it")
	}
}

// A kind that does have the link verbs is refused the same way, and this is
// the case that says why. localfs makes links and reads them back, but the
// port's only descriptive verb follows one: Stat on a link reports the entry it
// stands for, so there is no answer a mount could give the kernel that a later
// lookup would agree with.
func TestLinksAreRefusedEvenOnAKindThatHasTheVerbs(t *testing.T) {
	requireFUSE(t)

	host := t.TempDir()
	disk, err := localfs.New(host)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	dir := mount(t, disk)

	if err := os.WriteFile(filepath.Join(dir, "note"), []byte("linked to"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink("note", filepath.Join(dir, "link")); err == nil {
		t.Error("Symlink succeeded; the mount serves no links")
	}
}

// A link planted behind the mount is served as the entry it stands for. It is
// not hidden and it is not an error: reading it reads the target, through the
// plugin, which is the plugin's own containment and not the mount's.
func TestAPlantedLinkIsServedAsWhatItStandsFor(t *testing.T) {
	requireFUSE(t)

	host := t.TempDir()
	disk, err := localfs.New(host)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	if err := disk.Write(t.Context(), "/note", []byte("linked to")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := disk.Symlink(t.Context(), "/note", "/link"); err != nil {
		t.Fatalf("Symlink behind the mount: %v", err)
	}
	dir := mount(t, disk)

	info, err := os.Lstat(filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		t.Errorf("the mount reports %v, want an ordinary file", info.Mode())
	}

	read, err := os.ReadFile(filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(read) != "linked to" {
		t.Errorf("read %q, want %q", read, "linked to")
	}
}

// The mount is over whatever FileSystem it was handed, and a scoped view is
// one. What a caller inside writes lands under the scope's subtree, spelled by
// the scope and never by the mount.
func TestAMountOverAScopedView(t *testing.T) {
	requireFUSE(t)

	tree := memfs.New()
	scope := workspace.Scope{User: "alice", Workspace: "one"}
	root, err := scope.Path("/files", "")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := tree.Mkdir(t.Context(), root); err != nil {
		t.Fatalf("Mkdir the scope root: %v", err)
	}
	view, err := scope.View(tree, "/files")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	dir := mount(t, view)

	if err := os.WriteFile(filepath.Join(dir, "note"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stored, err := tree.Read(t.Context(), "/files/alice/one/note")
	if err != nil {
		t.Fatalf("the tree does not hold the scoped path: %v", err)
	}
	if string(stored) != "mine" {
		t.Errorf("the tree holds %q, want %q", stored, "mine")
	}

	// A caller inside the mount has no spelling for anything above the
	// scope root, so the tree outside it is untouched.
	if _, err := tree.Stat(t.Context(), "/files/note"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("something was written beside the scope: %v", err)
	}
}
