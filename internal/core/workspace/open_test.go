package workspace_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
	"github.com/tunedev/nerve/internal/vfstest"
)

// mustOpen is alice's whole workspace as a process inside it sees it.
func mustOpen(t *testing.T, life *workspace.Lifecycle, s workspace.Scope) vfs.FileSystem {
	t.Helper()
	fsys, err := life.Open(s)
	if err != nil {
		t.Fatalf("Open(%+v): %v", s, err)
	}
	return fsys
}

// inside is a path as a process in the workspace spells it: an area's name and
// whatever it spelled after it, uncleaned, so the filesystem is the thing that
// resolves the climb rather than the test.
func inside(area, rest string) string { return area + "/" + rest }

// spellings is every attempt to name something outside the workspace from
// inside it. The climbs are the ones every layer below is held to; the rest are
// this layer's own, and they name the layout rather than climbing out of it — a
// process that guessed the tree it stands in would spell the area root, the
// user segment and another user's workspace, and each of those has to be an
// ordinary name inside this workspace instead.
var spellings = append([]string{
	"bob/cv/secret",
	"files/bob/cv/secret",
	"/files/bob/cv/secret",
	"../bob/cv/secret",
	"/alice/cv/../../bob/cv/secret",
	"alice/cv/secret",
}, climbs...)

// newWorkspace is one empty workspace of alice's, over the kinds the areas are:
// a host directory, a tree in memory and a keyspace.
func newWorkspace(t *testing.T) vfs.FileSystem {
	t.Helper()

	table := vfs.NewMountTable()
	mustMount(t, table, files, mustLocal(t))
	mustMount(t, table, vectors, memfs.New())
	mustMount(t, table, keys, kvfs.New(kvfs.NewMemStore(), ""))

	life, err := workspace.NewLifecycle(table, isolatedAreas...)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	mustCreate(t, life, alice)
	return mustOpen(t, life, alice)
}

// An opened workspace is a FileSystem, so it answers to the suite every storage
// kind answers to rather than to cases written for it. It runs at an area,
// where the workspace is a tree, and at the root, where it is the read-only
// list of areas.
//
// The keyspace area is not run. An accessor takes no path, so a workspace
// answers one whenever any area does, and the suite would ask a keyspace for
// the streaming its neighbour has. That is the mount table's shape rather than
// this composition's, and the area answers the suite through its own view.
func TestWorkspaceConformance(t *testing.T) {
	for _, root := range []string{files, "/"} {
		t.Run(root, func(t *testing.T) {
			vfstest.Run(t, vfstest.Plugin{New: newWorkspace, Root: root})
		})
	}
}

// A workspace is its areas: every one the lifecycle covers stands at its own
// name, and there is nothing beside them. The user segment and the area root
// the layout put the workspace under have no name here at all.
func TestAWorkspaceIsItsAreasAndNothingElse(t *testing.T) {
	life, _ := newIsolated(t)
	fsys := mustOpen(t, life, alice)

	entries, err := fsys.ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", "/", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Errorf("the area %q is not a directory", entry.Name)
		}
		names = append(names, entry.Name)
	}
	if want := []string{"files", "kv", "vectors"}; !slices.Equal(names, want) {
		t.Errorf("ReadDir(%q) = %v, want %v", "/", names, want)
	}

	info, err := fsys.Stat(t.Context(), "/")
	if err != nil || !info.IsDir() {
		t.Errorf("Stat(%q) = %+v, %v, want the ground the areas stand on", "/", info, err)
	}
}

// An ordinary name inside an area is the scope's path under it, so a process
// writes "/files/note" and the layout stays where Path derived it.
func TestAWriteInsideAWorkspaceLandsAtTheScopesPath(t *testing.T) {
	life, table := newIsolated(t)
	fsys := mustOpen(t, life, alice)

	if err := fsys.Write(t.Context(), "/files/note", []byte("alice's draft")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	stored, err := table.Read(t.Context(), mustPath(t, alice, files, "note"))
	if err != nil {
		t.Fatalf("the tree does not hold the scoped path: %v", err)
	}
	if string(stored) != "alice's draft" {
		t.Errorf("the tree holds %q, want %q", stored, "alice's draft")
	}
}

// The acceptance, on the layer a process is handed: no path spelled inside
// alice's workspace reaches bob's, whatever the verb and whichever area it is
// spelled from. A climb is resolved before it is dispatched, so it lands on a
// name no area holds rather than on another scope.
func TestNoPathSpelledInsideAWorkspaceReachesAnother(t *testing.T) {
	for name, verb := range mutatingVerbs() {
		t.Run(name, func(t *testing.T) {
			for _, spelling := range spellings {
				life, table := newIsolated(t)
				modes := bobsModes(t, table)
				fsys := mustOpen(t, life, alice)
				for _, area := range isolatedAreas {
					_ = verb(t.Context(), fsys, inside(area, spelling))
				}
				requireBobUntouched(t, life, table, modes, name+" of "+spelling+" inside a workspace")
			}
		})
	}
}

// A read that climbs out of an area finds nothing rather than another user's
// content: the path it resolves to names no area of this workspace.
func TestNoReadInsideAWorkspaceReturnsAnothersContent(t *testing.T) {
	life, _ := newIsolated(t)
	fsys := mustOpen(t, life, alice)

	for _, spelling := range spellings {
		for _, area := range isolatedAreas {
			name := inside(area, spelling)
			content, err := fsys.Read(t.Context(), name)
			if err == nil {
				t.Errorf("Read(%q) = %q, want it to find nothing in the workspace", name, content)
			}
			if string(content) == bobsSecret {
				t.Errorf("Read(%q) returned bob's content", name)
			}
		}
	}
}

// Listing out of an area lands on the root, which names the areas. A user
// segment, another user's workspace and an entry in one are all names a
// listing inside a workspace never carries.
func TestNoListingInsideAWorkspaceNamesAnotherScope(t *testing.T) {
	life, _ := newIsolated(t)
	fsys := mustOpen(t, life, alice)

	for _, climb := range []string{"..", "../..", "../../bob", "/../../bob/cv", "a/../../.."} {
		for _, area := range isolatedAreas {
			name := inside(area, climb)
			entries, err := fsys.ReadDir(t.Context(), name)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				switch entry.Name {
				case alice.User, bob.User, alice.Workspace, "secret":
					t.Errorf("ReadDir(%q) named %q, want only the areas", name, entry.Name)
				}
			}
		}
	}
}

// The root is the list of areas and stays that list. A process that could make
// a name beside them would be naming a place the scope derives nothing for.
func TestTheRootOfAWorkspaceRefusesToGrowAName(t *testing.T) {
	life, _ := newIsolated(t)
	fsys := mustOpen(t, life, alice)

	for name, act := range mutatingVerbs() {
		if err := act(t.Context(), fsys, "/intruder"); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s of %q = %v, want fs.ErrPermission", name, "/intruder", err)
		}
	}
	if _, err := fsys.Read(t.Context(), "/intruder"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(%q) = %v, want fs.ErrNotExist", "/intruder", err)
	}
}

// An area is a mount, and no plugin can act on an entry it does not hold, so
// carrying one between areas is refused rather than half done.
func TestNoRenameCarriesAnEntryBetweenAreas(t *testing.T) {
	life, table := newIsolated(t)
	fsys := mustOpen(t, life, alice)

	err := fsys.Rename(t.Context(), "/files/own.md", "/vectors/own.md")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename between areas = %v, want fs.ErrInvalid", err)
	}
	if _, err := table.Read(t.Context(), mustPath(t, alice, files, "own.md")); err != nil {
		t.Errorf("the entry left its area: %v", err)
	}
}

// User scope is the subtree holding every workspace a user has, not a place
// inside one. Opening it would present one user's workspaces as areas.
func TestOpenRefusesAScopeNamingNoWorkspace(t *testing.T) {
	life, _ := newIsolated(t)

	if _, err := life.Open(workspace.Scope{User: alice.User}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Open of user scope = %v, want fs.ErrInvalid", err)
	}
	if _, err := life.Open(workspace.Scope{}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Open of the zero scope = %v, want fs.ErrInvalid", err)
	}
}

// An area is one name in the root here, so a nested one is refused rather than
// listed as a name that holds nothing. A lifecycle may still cover it.
func TestOpenRefusesAnAreaThatIsMoreThanOneName(t *testing.T) {
	life, err := workspace.NewLifecycle(vfs.NewMountTable(), files, "/index/vectors")
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	if _, err := life.Open(alice); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Open over a nested area = %v, want fs.ErrInvalid", err)
	}
}
