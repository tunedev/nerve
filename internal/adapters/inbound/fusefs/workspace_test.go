package fusefs_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The areas a workspace occupies here. Two is the smallest number that makes
// them siblings rather than a root, which is the whole of what this composition
// adds over a mount of one view.
var workspaceAreas = []string{"/files", "/vectors"}

var (
	inhabitant = workspace.Scope{User: "alice", Workspace: "cv"}
	neighbour  = workspace.Scope{User: "bob", Workspace: "cv"}
)

// theNeighboursSecret is what a leak looks like through the mount: content in
// no scope of alice's, so reading it back is unambiguous.
const theNeighboursSecret = "bob's salary expectations"

// mountWorkspace serves the whole of alice's workspace and returns the host
// path and the tree beneath it, which is where a leak is looked for.
func mountWorkspace(t *testing.T) (string, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	for _, area := range workspaceAreas {
		if err := table.Mount(area, memfs.New()); err != nil {
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

func scopePath(t *testing.T, s workspace.Scope, area, rest string) string {
	t.Helper()
	name, err := s.Path(area, rest)
	if err != nil {
		t.Fatalf("Path(%q, %q): %v", area, rest, err)
	}
	return name
}

func writeAt(t *testing.T, fsys vfs.FileSystem, name, content string) {
	t.Helper()
	if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", name, err)
	}
}

// requireTheNeighbourUntouched is the assertion every case here ends on: what
// bob holds is the same afterwards, and his workspace grew nothing.
func requireTheNeighbourUntouched(t *testing.T, table vfs.FileSystem, after string) {
	t.Helper()

	for _, area := range workspaceAreas {
		name := scopePath(t, neighbour, area, "secret")
		content, err := table.Read(t.Context(), name)
		if err != nil {
			t.Errorf("after %s: Read(%q) = %v, want bob's entry still there", after, name, err)
			continue
		}
		if string(content) != theNeighboursSecret {
			t.Errorf("after %s: Read(%q) = %q, want %q", after, name, content, theNeighboursSecret)
		}

		root := scopePath(t, neighbour, area, "")
		entries, err := table.ReadDir(t.Context(), root)
		if err != nil {
			t.Errorf("after %s: ReadDir(%q) = %v", after, root, err)
			continue
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
		if want := []string{"secret"}; !slices.Equal(names, want) {
			t.Errorf("after %s: ReadDir(%q) = %v, want %v", after, root, names, want)
		}
	}
}

// What the kernel is shown at the top of a workspace is the areas. There is no
// entry for the user segment, for the area root the layout put the workspace
// under, or for another user, because none of those is a name the mount serves.
func TestAWorkspaceMountShowsTheAreasAndNothingElse(t *testing.T) {
	dir, _ := mountWorkspace(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Errorf("the area %q is not a directory", entry.Name())
		}
		names = append(names, entry.Name())
	}
	if want := []string{"files", "vectors"}; !slices.Equal(names, want) {
		t.Errorf("ReadDir of the mount = %v, want %v", names, want)
	}
}

// An ordinary path inside an area is the scope's path under it, so a process
// that knows nothing of Nerve writes "files/note" and the layout stays where
// Path derived it.
func TestAWriteInsideAWorkspaceMountLandsAtTheScopesPath(t *testing.T) {
	dir, table := mountWorkspace(t)

	if err := os.WriteFile(filepath.Join(dir, "files", "note"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stored, err := table.Read(t.Context(), scopePath(t, inhabitant, "/files", "note"))
	if err != nil {
		t.Fatalf("the tree does not hold the scoped path: %v", err)
	}
	if string(stored) != "mine" {
		t.Errorf("the tree holds %q, want %q", stored, "mine")
	}
	requireTheNeighbourUntouched(t, table, "a write inside the mount")
}

// The acceptance, with the kernel as the caller: no name a process spells
// inside the mount addresses anything outside its workspace.
//
// The names are the layout's own rather than climbs. A path spelling ".." is
// resolved by the kernel against its own dentry tree and never reaches a
// filesystem at all, so a case built on one would be asserting Linux; what does
// reach here is a single component at a time, and these are the components a
// process that guessed the tree it stands in would send.
func TestNoNameSpelledInAWorkspaceMountReachesAnotherScope(t *testing.T) {
	for _, spelling := range []string{
		"files/bob/cv/secret",
		"files/alice/cv/secret",
		"files/files/bob/cv/secret",
		"vectors/bob/cv/secret",
		"files/../files/bob/cv/secret",
	} {
		t.Run(spelling, func(t *testing.T) {
			dir, table := mountWorkspace(t)
			name := filepath.Join(dir, filepath.FromSlash(spelling))

			content, err := os.ReadFile(name)
			if err == nil {
				t.Errorf("ReadFile(%q) = %q, want nothing of bob's reachable", spelling, content)
			}
			if strings.Contains(string(content), theNeighboursSecret) {
				t.Errorf("ReadFile(%q) returned bob's content", spelling)
			}

			// The write is attempted after the read for the same reason the
			// port-level cases attempt one: a verb that only ever adds reaches
			// another scope without reading anything out of it.
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err == nil {
				_ = os.WriteFile(name, []byte("alice was here"), 0o644)
			}
			requireTheNeighbourUntouched(t, table, "spelling "+spelling)
		})
	}
}

// The top of a workspace is the list of areas and stays that list. A process
// that could make a name beside them would be naming a place the scope derives
// nothing for.
func TestAWorkspaceMountRefusesANameBesideTheAreas(t *testing.T) {
	dir, _ := mountWorkspace(t)

	if err := os.WriteFile(filepath.Join(dir, "intruder"), []byte("x"), 0o644); !errors.Is(err, os.ErrPermission) {
		t.Errorf("WriteFile beside the areas = %v, want a permission error", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "intruder"), 0o755); !errors.Is(err, os.ErrPermission) {
		t.Errorf("Mkdir beside the areas = %v, want a permission error", err)
	}
	if err := os.Remove(filepath.Join(dir, "files")); err == nil {
		t.Error("Remove of an area succeeded, want the areas to stay the whole of the workspace")
	}
}

// An area is a mount, and no plugin can act on an entry it does not hold, so an
// entry carried between areas is refused rather than half moved.
func TestNoRenameCarriesAnEntryBetweenAreasThroughAMount(t *testing.T) {
	dir, table := mountWorkspace(t)

	from := filepath.Join(dir, "files", "note")
	if err := os.WriteFile(from, []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := os.Rename(from, filepath.Join(dir, "vectors", "note")); err == nil {
		t.Error("Rename between areas succeeded, want it refused")
	}
	if _, err := table.Read(t.Context(), scopePath(t, inhabitant, "/files", "note")); err != nil {
		t.Errorf("the entry left its area: %v", err)
	}
}
