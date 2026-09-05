package fusefs_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The escape suite. Three attempts are named — path traversal, symlink escape
// and handle reuse — and each is made by the party the mount exists to contain:
// a process holding nothing but a path inside it.
//
// The store here is a host directory rather than a tree in memory, which is the
// difference from the composition cases next door. memfs resolves a name the
// same way the derivation already did, so a case over one fails only when the
// derivation fails. localfs resolves through os.Root, and the kernel follows a
// link long after any name could have been cleaned. Only over a real store do
// the derivation layer and the store layer give different answers, and only
// then can a case tell them apart.

// mountHostBackedWorkspace serves the whole of alice's workspace over host
// directories and returns the mount point and the tree beneath it, which is
// where a leak is looked for and where a link is planted.
func mountHostBackedWorkspace(t *testing.T) (string, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	for _, area := range workspaceAreas {
		disk, err := localfs.New(t.TempDir())
		if err != nil {
			t.Fatalf("localfs.New for %q: %v", area, err)
		}
		if err := table.Mount(area, disk); err != nil {
			t.Fatalf("Mount(%q): %v", area, err)
		}
	}
	life, err := workspace.NewLifecycle(table, workspaceAreas...)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	for _, s := range []workspace.Scope{inhabitant, neighbour} {
		if err := life.Create(t.Context(), s); err != nil {
			t.Fatalf("Create(%+v): %v", s, err)
		}
	}
	for _, area := range workspaceAreas {
		writeAt(t, table, scopePath(t, neighbour, area, "secret"), theNeighboursSecret)
	}

	fsys, err := life.Open(inhabitant)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return mount(t, fsys), table
}

// theLayouts are spellings of the tree itself: what a process sends when it has
// guessed what it is standing in. Each is an ordinary name with no separator
// left to clean and nothing for the kernel to resolve away, so each arrives at
// the mount one component at a time and is answered by Nerve.
var theLayouts = []string{
	"files/bob/cv/secret",
	"files/alice/cv/secret",
	"files/files/bob/cv/secret",
	"files/bob/../bob/cv/secret",
	"vectors/bob/cv/secret",
}

// theClimbs are spellings that leave where they start. They are asserted only
// against a process whose root is the mount, because that is the only setup in
// which failing is Nerve's doing rather than the shape of the host tree — see
// TestAClimbSpelledFromOutsideAMountNeverEntersIt.
var theClimbs = []string{
	"../secret",
	"../../secret",
	"files/../../secret",
	"files/../../../../../../secret",
	"files/./../../secret",
	"/secret",
}

// spell joins a path the way a process types it rather than the way Go tidies
// it: filepath.Join cleans, and a cleaned climb is a different attempt from the
// one the case names.
func spell(dir, name string) string { return dir + "/" + filepath.FromSlash(name) }

// Attempt one: reach another scope by naming it. The names are the layout's own
// for the reason theLayouts says, and the store beneath is a real one, so a
// derivation that let a name through would be followed by a kernel that
// resolves it rather than by a tree that would have refused it anyway.
func TestNoTraversalOutOfAHostBackedWorkspaceMount(t *testing.T) {
	for _, spelling := range theLayouts {
		t.Run(spelling, func(t *testing.T) {
			dir, table := mountHostBackedWorkspace(t)
			name := spell(dir, spelling)

			content, err := os.ReadFile(name)
			if err == nil {
				t.Errorf("ReadFile(%q) = %q, want nothing outside the workspace reachable", spelling, content)
			}
			if strings.Contains(string(content), theNeighboursSecret) {
				t.Errorf("ReadFile(%q) returned bob's content", spelling)
			}

			// The write follows the read because a verb that only ever adds
			// reaches another scope without reading anything out of it.
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err == nil {
				_ = os.WriteFile(name, []byte("alice was here"), 0o644)
			}
			requireTheNeighbourUntouched(t, table, "traversal by "+spelling)
		})
	}
}

// The same attempt from a process whose root is the mount, which is the setup
// E11.2 said a traversal case needs. os.Root resolves with RESOLVE_IN_ROOT, so
// ".." at the top is clamped and an absolute name is rerooted — what a process
// would get after pivot_root, without a mount namespace to arrange it.
//
// The climbs here are the kernel refusing; the layouts are Nerve refusing. Both
// belong in one case because a process inside cannot tell which layer answered
// and neither can this suite, and a table of only the first would pass against
// a filesystem with no containment in it.
func TestNoTraversalOutOfAWorkspaceMountHeldAsItsOwnRoot(t *testing.T) {
	dir, table := mountHostBackedWorkspace(t)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close the root: %v", err)
		}
	}()

	for _, spelling := range append(append([]string{}, theClimbs...), theLayouts...) {
		opened, err := root.Open(filepath.FromSlash(spelling))
		if err == nil {
			t.Errorf("Open(%q) with the mount as the root succeeded, want it refused", spelling)
			if err := opened.Close(); err != nil {
				t.Errorf("Close(%q): %v", spelling, err)
			}
		}
	}
	requireTheNeighbourUntouched(t, table, "traversal with the mount as the root")
}

// The premise the two cases above are shaped by, asserted rather than assumed:
// a climb spelled by a process standing outside the mount is resolved above the
// mount point and reads the host, and the filesystem is never asked at all.
//
// This is not a hole in Nerve and closing it is not a filesystem's verb. It is
// what makes a traversal table written from outside worthless — every entry
// passes against a mount with no containment in it, because none of them
// reaches the mount — and it is why the suite spells the layout instead.
func TestAClimbSpelledFromOutsideAMountNeverEntersIt(t *testing.T) {
	dir, table := mountHostBackedWorkspace(t)

	// A file on the host beside the mount point, which is what a climb out of
	// the mount lands next to.
	beside := filepath.Join(filepath.Dir(dir), "secret")
	if err := os.WriteFile(beside, []byte(theNeighboursSecret), 0o644); err != nil {
		t.Fatalf("planting a file beside the mount: %v", err)
	}

	content, err := os.ReadFile(spell(dir, "files/../../secret"))
	if err != nil {
		t.Fatalf("a climb out of the mount = %v, want the host file it lands on", err)
	}
	if string(content) != theNeighboursSecret {
		t.Errorf("the climb read %q, want the host file beside the mount", content)
	}
	requireTheNeighbourUntouched(t, table, "a climb spelled from outside the mount")
}

// Attempt two: have the filesystem author a target the kernel will resolve
// somewhere else. This is the only escape a FUSE filesystem can emit — the
// kernel resolves the bytes Readlink returns in the host's vocabulary and not
// in the mount's — and this mount emits none, because it serves no link verbs.
//
// Every target below is one a caller could name freely if the verb existed: the
// host root, a climb out of the mount, and the neighbour's subtree spelled as
// the layout has it.
func TestNoLinkMadeInsideAWorkspaceMountLeavesIt(t *testing.T) {
	dir, table := mountHostBackedWorkspace(t)

	inside := filepath.Join(dir, "files")
	for i, target := range []string{
		"/etc/passwd",
		"../../../../secret",
		"../../bob/cv/secret",
		"/files/bob/cv/secret",
		"bob/cv/secret",
	} {
		link := filepath.Join(inside, fmt.Sprintf("peek%d", i))
		if err := os.Symlink(target, link); !errors.Is(err, syscall.ENOTSUP) {
			t.Errorf("Symlink to %q = %v, want ENOTSUP: the mount serves no link verbs", target, err)
		}
		if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("something stands at %q after the refused Symlink: %v", link, err)
		}
	}

	// A hard link is refused for its own reason and matters here for the same
	// one: two names for an entry is a second spelling of a location.
	if err := os.WriteFile(filepath.Join(inside, "own.md"), []byte("alice's draft"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Link(filepath.Join(inside, "own.md"), filepath.Join(inside, "hard")); err == nil {
		t.Error("Link succeeded; the mount has no verb for it")
	}

	// And nothing served is a link, so there is no target to read out of the
	// mount even for an entry the caller made itself.
	if _, err := os.Readlink(filepath.Join(inside, "own.md")); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("Readlink inside the mount = %v, want EINVAL: nothing here is a link", err)
	}
	requireTheNeighbourUntouched(t, table, "a link made inside the mount")
}

// Attempt three: keep a descriptor across the removal of the path it was opened
// through, and read what that path used to stand for.
//
// It fails, and what refuses it is the mount's cache settings rather than
// anything the descriptor does. Nothing is cached — passing any Options at all
// leaves both the entry and the attribute timeout nil — so the kernel
// revalidates before the read, the revalidation is a Getattr on a path that is
// gone, and the read fails before the open copy is consulted. The window this
// case would otherwise find opens the day somebody sets a timeout for speed.
func TestAHandleDoesNotOutliveTheEntryItWasOpenedOn(t *testing.T) {
	dir, table := mountHostBackedWorkspace(t)
	name := filepath.Join(dir, "files", "own.md")
	if err := os.WriteFile(name, []byte("alice's draft"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	held, err := os.Open(name)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := held.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	// The copy is taken here, so what follows is a descriptor holding the
	// content rather than one that has not looked yet.
	buf := make([]byte, len("alice's draft"))
	if _, err := held.ReadAt(buf, 0); err != nil {
		t.Fatalf("the read before the removal: %v", err)
	}

	if err := os.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if n, err := held.ReadAt(buf, 0); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("read through the handle after the removal = %q (%d bytes), %v, want fs.ErrNotExist", buf[:n], n, err)
	}
	if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of the removed path = %v, want fs.ErrNotExist", err)
	}
	requireTheNeighbourUntouched(t, table, "a handle held across a removal")
}

// The other half of handle reuse: a descriptor holding unflushed content whose
// path is removed before it closes. The content lands when the descriptor
// closes, and it lands at the path the descriptor was opened through — inside
// the scope. A removed entry comes back; nothing appears outside.
//
// close(2) reports success, which is what a caller sees for a write to a path
// that no longer exists. That is a statement about revocation and not about
// containment: what a descriptor already holds is closed by closing it.
func TestAWriteThroughARevokedHandleStaysInsideTheScope(t *testing.T) {
	dir, table := mountHostBackedWorkspace(t)
	name := filepath.Join(dir, "files", "own.md")
	if err := os.WriteFile(name, []byte("alice's draft"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	held, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	buf := make([]byte, len("alice's draft"))
	if _, err := held.ReadAt(buf, 0); err != nil {
		t.Fatalf("the read that takes the copy: %v", err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := held.WriteAt([]byte("ZZZ"), 0); err != nil {
		t.Fatalf("Write through the revoked handle: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	at := scopePath(t, inhabitant, "/files", "own.md")
	content, err := table.Read(t.Context(), at)
	if err != nil {
		t.Fatalf("Read(%q) after the flush: %v", at, err)
	}
	if want := "ZZZce's draft"; string(content) != want {
		t.Errorf("the flush left %q at %q, want %q", content, at, want)
	}
	requireTheNeighbourUntouched(t, table, "a write flushed through a revoked handle")
}

// Where the symlink boundary is, rather than where it should be. A link
// standing inside alice's subtree whose target is bob's is followed, and the
// mount serves what it lands on.
//
// Nothing in this file's other cases can prevent it, and neither can the mount:
// the kernel resolves the link inside the syscall localfs makes, long after any
// name was derived, and os.Root's containment root is the area rather than the
// scope subtree. The view sees a link only in Readlink, which it does refuse.
//
// The process inside cannot plant one — Symlink is ENOTSUP through the mount
// and the view derives the target — so this is not an escape the sandbox emits.
// It is one the platform can hand it, and localfs's backing directory is an
// ordinary host directory that says so. Filed as nrv-aeyc; this case is what
// fails when it is fixed.
func TestALinkPlantedBehindAWorkspaceMountIsFollowedOutOfTheScope(t *testing.T) {
	dir, table := mountHostBackedWorkspace(t)

	planted := table.AsSymlinker()
	if planted == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed areas to answer")
	}
	if err := planted.Symlink(t.Context(),
		scopePath(t, neighbour, "/files", ""),
		scopePath(t, inhabitant, "/files", "peek"),
	); err != nil {
		t.Fatalf("planting the link behind the mount: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "files", "peek", "secret"))
	if err != nil {
		t.Fatalf("read through the planted link = %v, want the boundary where this case says it is", err)
	}
	if string(content) != theNeighboursSecret {
		t.Fatalf("read through the planted link = %q, want %q", content, theNeighboursSecret)
	}
}
