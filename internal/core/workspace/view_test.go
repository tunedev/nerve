package workspace_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
	"github.com/tunedev/nerve/internal/vfstest"
)

// A view is a FileSystem, so it answers to the suite every storage kind answers
// to rather than to a set of cases written for it.
//
// It runs over both kinds the isolation cases use. A tree in memory resolves a
// path the way the view already did, and a host-backed one does not: the rules
// a view keeps at its own root are the ones where the two answer differently,
// so a guard that a tree in memory refuses anyway is still asserted.
func TestConformance(t *testing.T) {
	for name, beneath := range map[string]func(t *testing.T) vfs.FileSystem{
		"over a tree in memory": func(*testing.T) vfs.FileSystem { return memfs.New() },
		"over a host directory": mustLocal,
		"over a keyspace":       func(*testing.T) vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "") },
	} {
		t.Run(name, func(t *testing.T) {
			vfstest.Run(t, vfstest.Plugin{
				New: func(t *testing.T) vfs.FileSystem {
					table := vfs.NewMountTable()
					mustMount(t, table, files, beneath(t))
					mustMkdirAt(t, table, mustPath(t, alice, files, ""))
					return mustView(t, alice, table, files)
				},
			})
		})
	}
}

// The paths a view answers for are the scope's, so a caller inside one writes
// ordinary names and the layout stays where it was derived.
func TestAWriteThroughAViewLandsInsideTheScope(t *testing.T) {
	table := vfs.NewMountTable()
	mustMount(t, table, files, memfs.New())
	mustMkdirAt(t, table, mustPath(t, alice, files, ""))

	view := mustView(t, alice, table, files)
	if err := view.Mkdir(t.Context(), "notes"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := view.Write(t.Context(), "notes/one.md", []byte("drafted")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	content, err := table.Read(t.Context(), "/files/alice/cv/notes/one.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := "drafted"; string(content) != want {
		t.Errorf("Read = %q, want %q", content, want)
	}
}

// Readlink answers in the view's vocabulary. A target the view spelled is a
// target the view can name back.
func TestReadlinkThroughAViewAnswersInTheViewsNames(t *testing.T) {
	_, table := newIsolated(t)
	view := mustView(t, alice, table, files)
	links := view.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer through the view")
	}
	if err := links.Symlink(t.Context(), "own.md", "peek"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	target, err := links.Readlink(t.Context(), "peek")
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if want := "/own.md"; target != want {
		t.Errorf("Readlink = %q, want %q", target, want)
	}
}

// A link planted behind the view, standing for a path outside the scope, has no
// name inside it. Handing the spelling back would make another user's layout
// observable to a caller that cannot reach it.
func TestReadlinkThroughAViewRefusesATargetOutsideTheScope(t *testing.T) {
	_, table := newIsolated(t)
	planted := table.AsSymlinker()
	if planted == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer")
	}
	if err := planted.Symlink(t.Context(), mustPath(t, bob, files, ""), mustPath(t, alice, files, "peek")); err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	links := mustView(t, alice, table, files).AsSymlinker()
	if _, err := links.Readlink(t.Context(), "peek"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Readlink of a link out of the scope = %v, want fs.ErrInvalid", err)
	}
}

// The verb this exists for: a link inside the scope is described as a link
// rather than as the entry it stands for, which is what a caller has to know
// before it decides whether to follow one.
func TestLstatThroughAViewDescribesTheLink(t *testing.T) {
	_, table := newIsolated(t)
	view := mustView(t, alice, table, files)
	links := view.AsSymlinker()
	if links == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer through the view")
	}
	if err := view.Write(t.Context(), "own.md", []byte("drafted")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := links.Symlink(t.Context(), "own.md", "peek"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	stood, err := view.Stat(t.Context(), "peek")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stood.IsSymlink() {
		t.Errorf("Stat mode = %v, want the entry the link stands for", stood.Mode)
	}

	found, err := links.Lstat(t.Context(), "peek")
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if !found.IsSymlink() {
		t.Errorf("Lstat mode = %v, want a link", found.Mode)
	}
	if found.Name != "peek" {
		t.Errorf("Lstat name = %q, want %q", found.Name, "peek")
	}
}

// A link planted behind the view, standing outside the scope, is still a link
// and is described as one. Where it stands for is the answer that is refused,
// and Readlink is the only verb that gives it: describing an entry says what it
// is, never another user's layout.
func TestLstatThroughAViewDescribesALinkOutOfTheScopeWithoutNamingItsTarget(t *testing.T) {
	_, table := newIsolated(t)
	planted := table.AsSymlinker()
	if planted == nil {
		t.Fatal("AsSymlinker = nil, want the host-backed area to answer")
	}
	if err := planted.Symlink(t.Context(), mustPath(t, bob, files, ""), mustPath(t, alice, files, "peek")); err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	links := mustView(t, alice, table, files).AsSymlinker()
	found, err := links.Lstat(t.Context(), "peek")
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if !found.IsSymlink() {
		t.Errorf("Lstat mode = %v, want a link", found.Mode)
	}
	if _, err := links.Readlink(t.Context(), "peek"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Readlink of a link out of the scope = %v, want fs.ErrInvalid", err)
	}
}

// An accessor the tree beneath refuses is refused through the view too, so the
// caller's nil guard keeps telling the truth.
func TestAViewAnswersNilForAnAccessorTheTreeBeneathHasNot(t *testing.T) {
	table := vfs.NewMountTable()
	mustMount(t, table, files, memfs.New())

	if links := mustView(t, alice, table, files).AsSymlinker(); links != nil {
		t.Errorf("AsSymlinker = %#v, want nil over a tree with no link verbs", links)
	}
}

// A view over a scope that cannot derive a path is refused where it is built,
// not at the first verb: a view is composition, and invalid composition is a
// startup failure.
func TestViewRefusesWhatItCannotStandOn(t *testing.T) {
	for name, build := range map[string]func() (vfs.FileSystem, error){
		"a scope with no user":         func() (vfs.FileSystem, error) { return workspace.Scope{}.View(memfs.New(), files) },
		"a user carrying a separator":  func() (vfs.FileSystem, error) { return workspace.Scope{User: "a/b"}.View(memfs.New(), files) },
		"a root that is not rooted":    func() (vfs.FileSystem, error) { return alice.View(memfs.New(), "files") },
		"a root that is not clean":     func() (vfs.FileSystem, error) { return alice.View(memfs.New(), "/files/") },
		"a root that climbs elsewhere": func() (vfs.FileSystem, error) { return alice.View(memfs.New(), "/files/../kv") },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := build(); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("View = %v, want fs.ErrInvalid", err)
			}
		})
	}
}

// Two views of the same scope over different areas answer for different trees,
// which is what makes an area a view's business rather than a caller's.
func TestAViewIsOneAreaOfOneScope(t *testing.T) {
	table := vfs.NewMountTable()
	mustMount(t, table, files, memfs.New())
	mustMount(t, table, vectors, memfs.New())
	for _, area := range []string{files, vectors} {
		mustMkdirAt(t, table, mustPath(t, alice, area, ""))
	}

	if err := mustView(t, alice, table, files).Write(t.Context(), "one", []byte("in files")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := mustView(t, alice, table, vectors).Read(t.Context(), "one"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read in the vectors area = %v, want fs.ErrNotExist", err)
	}
}
