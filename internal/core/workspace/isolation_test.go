package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The areas an isolation test spans. /files is host-backed on purpose. It is
// the only kind here whose containment the kernel enforces, so it is the only
// one where the derivation layer and the store layer give different answers and
// can be told apart.
var isolatedAreas = []string{files, vectors, keys}

// alice reads her own workspace; bob's is what she must not reach. Both are
// named "cv" so that a spelling which drops or crosses a user segment lands on
// a path that exists rather than on one that would have failed anyway.
var (
	alice = workspace.Scope{User: "alice", Workspace: "cv"}
	bob   = workspace.Scope{User: "bob", Workspace: "cv"}
)

// bobsSecret is what a leak looks like: content that appears in no scope of
// alice's, so reading it back is unambiguous.
const bobsSecret = "bob's salary expectations"

// newIsolated returns a table holding one workspace for each user, with bob's
// secret written in every area. The files area is host-backed so the store
// layer is real rather than a tree that resolves paths the way the derivation
// already did.
func newIsolated(t *testing.T) (*workspace.Lifecycle, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	mustMount(t, table, files, mustLocal(t))
	mustMount(t, table, vectors, memfs.New())
	mustMount(t, table, keys, kvfs.New(kvfs.NewMemStore(), ""))

	life, err := workspace.NewLifecycle(table, isolatedAreas...)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	for _, s := range []workspace.Scope{alice, bob} {
		mustCreate(t, life, s)
	}
	for _, name := range entriesOf(t, bob) {
		mustWriteAt(t, table, name, bobsSecret)
	}
	mustWriteAt(t, table, mustPath(t, alice, files, "own.md"), "alice's draft")
	return life, table
}

// modeOf is bob's entry as it stands before alice acts, so a verb that changed
// only the permission bits is caught alongside one that changed the content.
func modeOf(t *testing.T, fsys vfs.FileSystem, name string) fs.FileMode {
	t.Helper()
	info, err := fsys.Stat(t.Context(), name)
	if err != nil {
		t.Fatalf("Stat(%q): %v", name, err)
	}
	return info.Mode
}

// bobsModes captures what every one of his entries is set to.
func bobsModes(t *testing.T, fsys vfs.FileSystem) map[string]fs.FileMode {
	t.Helper()
	modes := map[string]fs.FileMode{}
	for _, name := range entriesOf(t, bob) {
		modes[name] = modeOf(t, fsys, name)
	}
	return modes
}

func mustLocal(t *testing.T) vfs.FileSystem {
	t.Helper()
	fsys, err := localfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	return fsys
}

func mustPath(t *testing.T, s workspace.Scope, root, rest string) string {
	t.Helper()
	name, err := s.Path(root, rest)
	if err != nil {
		t.Fatalf("Path(%q, %q) for %+v: %v", root, rest, s, err)
	}
	return name
}

// entriesOf is where a user's secret sits in each area.
func entriesOf(t *testing.T, s workspace.Scope) []string {
	t.Helper()
	names := make([]string, 0, len(isolatedAreas))
	for _, area := range isolatedAreas {
		names = append(names, mustPath(t, s, area, "secret"))
	}
	return names
}

// requireBobUntouched is the assertion every case ends on. A verb that reached
// bob shows up here as content that changed, permission bits that changed, an
// entry that vanished, or a workspace that stopped being listed — whichever of
// the four that verb would have caused.
func requireBobUntouched(t *testing.T, life *workspace.Lifecycle, fsys vfs.FileSystem, modes map[string]fs.FileMode, after string) {
	t.Helper()

	for _, name := range entriesOf(t, bob) {
		content, err := fsys.Read(t.Context(), name)
		if err != nil {
			t.Errorf("after %s: Read(%q) = %v, want bob's entry still there", after, name, err)
			continue
		}
		if string(content) != bobsSecret {
			t.Errorf("after %s: Read(%q) = %q, want %q", after, name, content, bobsSecret)
		}
		if got := modeOf(t, fsys, name); got != modes[name] {
			t.Errorf("after %s: mode of %q = %v, want %v", after, name, got, modes[name])
		}
	}
	// What bob's workspace holds, not merely that what he put there survives:
	// an entry alice added is observable to him even though it destroyed
	// nothing of his.
	root := mustPath(t, bob, files, "")
	entries, err := fsys.ReadDir(t.Context(), root)
	if err != nil {
		t.Errorf("after %s: ReadDir(%q) = %v", after, root, err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if want := []string{"secret"}; !slices.Equal(names, want) {
		t.Errorf("after %s: ReadDir(%q) = %v, want %v", after, root, names, want)
	}

	got, err := life.List(t.Context(), bob.User)
	if err != nil {
		t.Errorf("after %s: List(%q) = %v", after, bob.User, err)
		return
	}
	if want := []string{bob.Workspace}; !slices.Equal(got, want) {
		t.Errorf("after %s: List(%q) = %v, want %v", after, bob.User, got, want)
	}
}

// climbs is every spelling that tries to leave one scope for another. Each is
// a rest inside alice's scope and a path inside a view of it, so the same table
// asks the question of the derivation layer and of the view.
var climbs = []string{
	"../../bob/cv/secret",
	"../../../files/bob/cv/secret",
	"/../../bob/cv/secret",
	"drafts/../../../bob/cv/secret",
	"../../bob/cv",
	// A leaf bob does not have, so a verb that only ever adds is caught
	// alongside the ones that overwrite or delete.
	"../../bob/cv/intruder",
	"..",
}

// The acceptance, on the layer that answers for it: no rest a caller spells
// inside alice's scope reaches bob's, whatever the verb. Each verb runs against
// a fresh world because several of them destroy alice's own, and a verb that
// found nothing left of hers would prove nothing about his.
func TestNoRestSpelledInsideOneScopeReachesAnother(t *testing.T) {
	for name, verb := range mutatingVerbs() {
		t.Run(name, func(t *testing.T) {
			for _, climb := range climbs {
				life, fsys := newIsolated(t)
				modes := bobsModes(t, fsys)
				for _, area := range isolatedAreas {
					// The error is not asserted. A verb that lands inside
					// alice's scope may succeed there or fail on a path she
					// does not have, and both are contained; what is asserted
					// is that bob's world is the same afterwards.
					_ = verb(t.Context(), fsys, mustPath(t, alice, area, climb))
				}
				requireBobUntouched(t, life, fsys, modes, name+" of "+climb)
			}
		})
	}
}

// mutatingVerbs is every verb on the port that could change what another user
// holds. Chmod is here for the same reason Remove is: it needs no content of
// its own to make one user's entry observably different for another.
func mutatingVerbs() map[string]func(context.Context, vfs.FileSystem, string) error {
	return map[string]func(context.Context, vfs.FileSystem, string) error{
		"Write": func(ctx context.Context, f vfs.FileSystem, name string) error {
			return f.Write(ctx, name, []byte("alice was here"))
		},
		"Create":    func(ctx context.Context, f vfs.FileSystem, name string) error { return f.Create(ctx, name) },
		"Mkdir":     func(ctx context.Context, f vfs.FileSystem, name string) error { return f.Mkdir(ctx, name) },
		"Remove":    func(ctx context.Context, f vfs.FileSystem, name string) error { return f.Remove(ctx, name) },
		"RemoveAll": func(ctx context.Context, f vfs.FileSystem, name string) error { return f.RemoveAll(ctx, name) },
		// 0o700 rather than something that clears the execute bit: a climb
		// that lands on alice's own workspace directory would otherwise leave
		// a tree the test framework cannot clean up, which is the harness
		// failing rather than the property.
		"Chmod": func(ctx context.Context, f vfs.FileSystem, name string) error { return f.Chmod(ctx, name, 0o700) },
		"RenameOnto": func(ctx context.Context, f vfs.FileSystem, name string) error {
			return f.Rename(ctx, name, name+".taken")
		},
	}
}

// Rename is the one verb taking two paths, so it has a direction the table
// above cannot spell: one end anchored in alice's real scope and the other
// climbing. Neither moves an entry across, in either direction.
func TestNoRenameCarriesAnEntryBetweenScopes(t *testing.T) {
	for name, ends := range map[string]func(t *testing.T) (string, string){
		"out of alice's scope": func(t *testing.T) (string, string) {
			return mustPath(t, alice, files, "own.md"), mustPath(t, alice, files, "../../bob/cv/taken")
		},
		"into alice's scope": func(t *testing.T) (string, string) {
			return mustPath(t, alice, files, "../../bob/cv/secret"), mustPath(t, alice, files, "stolen")
		},
	} {
		t.Run(name, func(t *testing.T) {
			life, fsys := newIsolated(t)
			modes := bobsModes(t, fsys)
			from, to := ends(t)

			_ = fsys.Rename(t.Context(), from, to)

			requireBobUntouched(t, life, fsys, modes, "renaming "+name)
			if content, err := fsys.Read(t.Context(), mustPath(t, alice, files, "stolen")); err == nil {
				t.Errorf("alice holds %q after the rename, want nothing carried across", content)
			}
		})
	}
}

// The other half of the acceptance: a read that climbs returns what alice's own
// scope holds at that path, which is nothing, rather than what bob's does.
func TestNoReadSpelledInsideOneScopeReturnsAnothersContent(t *testing.T) {
	_, fsys := newIsolated(t)

	for _, climb := range []string{
		"../../bob/cv/secret",
		"../../../files/bob/cv/secret",
		"/../../bob/cv/secret",
		"drafts/../../../bob/cv/secret",
	} {
		for _, area := range isolatedAreas {
			name := mustPath(t, alice, area, climb)
			content, err := fsys.Read(t.Context(), name)
			if err == nil {
				t.Errorf("Read(%q) = %q, want it to find nothing in alice's scope", name, content)
			}
			if string(content) == bobsSecret {
				t.Errorf("Read(%q) returned bob's content", name)
			}
		}
	}
}

// Listing a directory that climbs must not name another user's workspaces
// either: a name is observable even when the content behind it is not.
func TestNoDirectoryListingInsideOneScopeNamesAnothersWorkspace(t *testing.T) {
	_, fsys := newIsolated(t)

	for _, climb := range []string{"../..", "../../bob", "/../../bob/cv", "a/../../.."} {
		name := mustPath(t, alice, files, climb)
		entries, err := fsys.ReadDir(t.Context(), name)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Name == bob.User || entry.Name == "secret" {
				t.Errorf("ReadDir(%q) named %q, want only what is inside alice's scope", name, entry.Name)
			}
		}
	}
}

// A user's own listing answers for their subtree and no other, which is what
// makes List safe to show somebody.
func TestListNamesNoOtherUsersWorkspaces(t *testing.T) {
	life, _ := newIsolated(t)
	mustCreate(t, life, workspace.Scope{User: bob.User, Workspace: "notes"})

	got, err := life.List(t.Context(), alice.User)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []string{alice.Workspace}; !slices.Equal(got, want) {
		t.Errorf("List(%q) = %v, want %v", alice.User, got, want)
	}
}

// Deleting one user's workspace is the loudest way to be observable from
// another's, and it runs here over a host-backed area rather than a tree in
// memory: a RemoveAll that resolved a path before deleting it would take the
// other user with it.
func TestDeletingOneUsersWorkspaceLeavesAnothersWhole(t *testing.T) {
	life, fsys := newIsolated(t)

	modes := bobsModes(t, fsys)
	if err := life.Delete(t.Context(), alice); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	requireBobUntouched(t, life, fsys, modes, "deleting alice's workspace")
	if left, err := life.List(t.Context(), alice.User); err != nil || len(left) != 0 {
		t.Errorf("List(%q) after delete = %v, %v, want no workspaces", alice.User, left, err)
	}
}

// A link is the verb that reaches past a string, so a scope-derived one is
// worth its own case: the climb in the target is clamped before the link is
// made, and what alice ends up with points inside her own workspace.
func TestALinkDerivedThroughAScopeStaysInsideIt(t *testing.T) {
	_, fsys := newIsolated(t)
	links := fsys.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer")
	}

	link := mustPath(t, alice, files, "peek")
	target := mustPath(t, alice, files, "../../bob/cv")
	if err := links.Symlink(t.Context(), target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, err := fsys.Read(t.Context(), link+"/secret"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read through a scope-derived link = %v, want fs.ErrNotExist", err)
	}
}

// The mount table holds the mount boundary: a link cannot join two areas, so no
// area reaches into another whatever either end names.
//
// Both areas are host-backed here, which is what makes the case ask the
// question. Against a kind with no link verbs the refusal comes from the kind
// rather than the boundary, and the case would pass with the boundary gone.
func TestALinkCannotJoinTwoAreas(t *testing.T) {
	table := vfs.NewMountTable()
	mustMount(t, table, files, mustLocal(t))
	mustMount(t, table, vectors, mustLocal(t))
	for _, area := range []string{files, vectors} {
		for _, s := range []workspace.Scope{alice, bob} {
			mustMkdirAt(t, table, mustPath(t, s, area, ""))
		}
	}
	mustWriteAt(t, table, mustPath(t, bob, vectors, "secret"), bobsSecret)

	links := table.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed areas to answer")
	}

	// Each target gets its own link name. Reusing one would let the first
	// refusal be indistinguishable from an entry that already exists, and the
	// case would pass with the mount boundary gone.
	for i, target := range []string{mustPath(t, bob, vectors, ""), mustPath(t, bob, vectors, "secret"), "/etc"} {
		link := mustPath(t, alice, files, fmt.Sprintf("peek%d", i))
		if err := links.Symlink(t.Context(), target, link); err == nil {
			t.Errorf("Symlink(%q, %q) = nil, want a link across a mount refused", target, link)
		}
	}
}

// Why a caller is handed a view and not the table: no layer beneath one holds
// the scope root inside a single mount.
//
// Scope.Path contains a rest lexically, to the scope root, which every case
// above exercises. The mount table contains a link to the mount prefix, which
// TestALinkCannotJoinTwoAreas exercises. localfs contains a path to its own
// root through os.Root, which the conformance suite's escape cases exercise by
// calling the plugin directly — through a mount table they cannot, because
// resolve cleans and rejects before the plugin is asked.
//
// A target under another user's subtree in the same mount satisfies all three:
// the path is clean, both ends are in one mount, and the target is inside the
// localfs root. Symlink takes a second path, and a caller holding the table
// spells that one itself.
//
// So the containment is the view's. The cases below hold it there.
func TestTheMountTableAloneDoesNotHoldTheScopeBoundary(t *testing.T) {
	_, fsys := newIsolated(t)
	links := fsys.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer")
	}

	// A target no derivation of alice's scope produces, inside the mount she
	// shares with bob.
	link := mustPath(t, alice, files, "peek")
	if err := links.Symlink(t.Context(), mustPath(t, bob, files, ""), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	content, err := fsys.Read(t.Context(), link+"/secret")
	if err != nil {
		t.Fatalf("Read through the link = %v, want the boundary to be where this test says it is", err)
	}
	if string(content) != bobsSecret {
		t.Fatalf("Read through the link = %q, want %q", content, bobsSecret)
	}
}

// The other half of the same statement: a link the table planted is not one a
// view follows. A caller holding the table can spell a target under bob's
// subtree, as the case above does; a caller holding a view of alice's scope is
// served nothing through it.
//
// Every verb is asserted rather than only Readlink, because Readlink is the one
// place a view sees a link at all — Read, Stat and ReadDir are answered by
// whatever resolved the name, and over a host disk that is a kernel resolving
// the link inside the syscall.
func TestALinkPlantedBehindAViewIsNotFollowedOutOfTheScope(t *testing.T) {
	_, table := newIsolated(t)
	links := table.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer")
	}
	if err := links.Symlink(t.Context(), mustPath(t, bob, files, ""), mustPath(t, alice, files, "peek")); err != nil {
		t.Fatalf("planting the link behind the view: %v", err)
	}

	view := mustView(t, alice, table, files)
	if content, err := view.Read(t.Context(), "/peek/secret"); err == nil {
		t.Errorf("Read through the planted link = %q, want it refused", content)
	}
	if found, err := view.Stat(t.Context(), "/peek"); err == nil {
		t.Errorf("Stat through the planted link = %+v, want it refused", found)
	}
	if entries, err := view.ReadDir(t.Context(), "/peek"); err == nil {
		t.Errorf("ReadDir through the planted link = %v, want it refused", entries)
	}
	if target, err := view.AsSymlinker().Readlink(t.Context(), "/peek"); err == nil {
		t.Errorf("Readlink of the planted link = %q, want it refused", target)
	}
}

// mustView is what a caller holding one scope is given instead of the table:
// the area as it is seen from inside the scope.
func mustView(t *testing.T, s workspace.Scope, fsys vfs.FileSystem, area string) vfs.FileSystem {
	t.Helper()
	view, err := s.View(fsys, area)
	if err != nil {
		t.Fatalf("View(%q) for %+v: %v", area, s, err)
	}
	return view
}

// The acceptance, on the layer that now answers for it: no path a caller spells
// inside a view of alice's scope reaches bob's, whatever the verb. The table is
// the same one, so a verb that got through would be visible to him exactly as
// it is above.
func TestNoPathSpelledInsideAScopedViewReachesAnotherScope(t *testing.T) {
	for name, verb := range mutatingVerbs() {
		t.Run(name, func(t *testing.T) {
			for _, climb := range climbs {
				life, table := newIsolated(t)
				modes := bobsModes(t, table)
				for _, area := range isolatedAreas {
					_ = verb(t.Context(), mustView(t, alice, table, area), climb)
				}
				requireBobUntouched(t, life, table, modes, name+" of "+climb+" through a view")
			}
		})
	}
}

// The spelling nothing below the view contains: a link target under another
// user's subtree in the same mount. The caller does not derive it, so there is
// no spelling of it left — the view derives both ends.
func TestALinkMadeThroughAScopedViewCannotReachAnotherScope(t *testing.T) {
	_, table := newIsolated(t)
	view := mustView(t, alice, table, files)
	links := view.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer through the view")
	}

	for i, target := range []string{
		mustPath(t, bob, files, ""),
		mustPath(t, bob, files, "secret"),
		"../../bob/cv",
		"/../../bob/cv",
	} {
		link := fmt.Sprintf("peek%d", i)
		if err := links.Symlink(t.Context(), target, link); err != nil {
			t.Fatalf("Symlink(%q, %q): %v", target, link, err)
		}
		content, err := view.Read(t.Context(), path.Join(link, "secret"))
		if err == nil {
			t.Errorf("Read through a link to %q = %q, want nothing of bob's reachable", target, content)
		}
		if string(content) == bobsSecret {
			t.Errorf("Read through a link to %q returned bob's content", target)
		}
	}
}

// A read through a view answers with what alice's own scope holds at that path,
// which is nothing, rather than with what bob's does.
func TestNoReadInsideAScopedViewReturnsAnothersContent(t *testing.T) {
	_, table := newIsolated(t)

	for _, climb := range climbs {
		for _, area := range isolatedAreas {
			view := mustView(t, alice, table, area)
			content, err := view.Read(t.Context(), climb)
			if err == nil {
				t.Errorf("Read(%q) in %q = %q, want it to find nothing in alice's scope", climb, area, content)
			}
			if string(content) == bobsSecret {
				t.Errorf("Read(%q) in %q returned bob's content", climb, area)
			}
		}
	}
}

// Listing through a view must not name another user's workspaces either: a name
// is observable even when the content behind it is not.
func TestNoListingInsideAScopedViewNamesAnotherScope(t *testing.T) {
	_, table := newIsolated(t)
	view := mustView(t, alice, table, files)

	for _, climb := range []string{"/", "../..", "../../bob", "/../../bob/cv", "a/../../.."} {
		entries, err := view.ReadDir(t.Context(), climb)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Name == bob.User || entry.Name == "secret" {
				t.Errorf("ReadDir(%q) named %q, want only what is inside alice's scope", climb, entry.Name)
			}
		}
	}
}

// Rename is the other verb taking two paths, and a view derives both, so
// neither end can be anchored outside the scope however it is spelled.
func TestNoRenameThroughAScopedViewCarriesAnEntryBetweenScopes(t *testing.T) {
	for name, ends := range map[string][2]string{
		"out of alice's scope": {"own.md", "../../bob/cv/taken"},
		"into alice's scope":   {"../../bob/cv/secret", "stolen"},
	} {
		t.Run(name, func(t *testing.T) {
			life, table := newIsolated(t)
			modes := bobsModes(t, table)
			view := mustView(t, alice, table, files)

			_ = view.Rename(t.Context(), ends[0], ends[1])

			requireBobUntouched(t, life, table, modes, "renaming "+name+" through a view")
			if content, err := view.Read(t.Context(), "stolen"); err == nil {
				t.Errorf("alice holds %q after the rename, want nothing carried across", content)
			}
		})
	}
}
