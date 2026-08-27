package workspace_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/synth"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The areas a workspace occupies in these tests. Three are trees and one is a
// keyspace, which is the pair of shapes the port has: what a lifecycle does to
// a vector index or a graph it does to any tree, and kvfs is the kind that is
// not one. A queue is a tree here rather than queuefs, because what is being
// read is the path a delete reaches and not what a broker does with it.
const (
	files   = "/files"
	vectors = "/vectors"
	graph   = "/graph"
	keys    = "/kv"
)

func newLifecycle(t *testing.T) (*workspace.Lifecycle, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	for _, area := range []string{files, vectors, graph} {
		mustMount(t, table, area, memfs.New())
	}
	mustMount(t, table, keys, kvfs.New(kvfs.NewMemStore(), ""))

	life, err := workspace.NewLifecycle(table, files, vectors, graph, keys)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	return life, table
}

func mustMount(t *testing.T, table *vfs.MountTable, prefix string, fsys vfs.FileSystem) {
	t.Helper()
	if err := table.Mount(prefix, fsys); err != nil {
		t.Fatalf("Mount(%q): %v", prefix, err)
	}
}

func mustCreate(t *testing.T, life *workspace.Lifecycle, s workspace.Scope) {
	t.Helper()
	if err := life.Create(t.Context(), s); err != nil {
		t.Fatalf("Create(%+v): %v", s, err)
	}
}

// fill puts one entry in every area of s, so that a delete has something to
// remove everywhere rather than only where the test happened to look.
func fill(t *testing.T, fsys vfs.FileSystem, s workspace.Scope, content string) {
	t.Helper()
	for _, name := range filled(t, s) {
		if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
			t.Fatalf("Write(%q): %v", name, err)
		}
	}
}

// filled is where fill wrote.
func filled(t *testing.T, s workspace.Scope) []string {
	t.Helper()
	names := make([]string, 0, 4)
	for _, area := range []string{files, vectors, graph, keys} {
		name, err := s.Path(area, "entry")
		if err != nil {
			t.Fatalf("Path(%q): %v", area, err)
		}
		names = append(names, name)
	}
	return names
}

func TestCreateMakesTheWorkspaceInEveryTreeArea(t *testing.T) {
	life, fsys := newLifecycle(t)
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	mustCreate(t, life, s)

	for _, area := range []string{files, vectors, graph} {
		name, err := s.Path(area, "")
		if err != nil {
			t.Fatalf("Path(%q): %v", area, err)
		}
		info, err := fsys.Stat(t.Context(), name)
		if err != nil {
			t.Fatalf("Stat(%q) = %v, want a directory", name, err)
		}
		if !info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = false, want true", name)
		}
	}
}

// A keyspace has no directory to make, so a created workspace does not exist
// there until something is written under it. That is the descriptor speaking,
// not a create that failed.
func TestCreateInAKeyspaceMakesNothingAndSucceeds(t *testing.T) {
	life, fsys := newLifecycle(t)
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	mustCreate(t, life, s)

	name, err := s.Path(keys, "")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := fsys.Stat(t.Context(), name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%q) = %v, want fs.ErrNotExist", name, err)
	}
}

// Idempotent means the second create is the first one: a caller that cannot
// remember whether it created a workspace creates it again and keeps what is
// in it.
func TestCreateTwiceKeepsWhatIsInTheWorkspace(t *testing.T) {
	life, fsys := newLifecycle(t)
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	mustCreate(t, life, s)
	fill(t, fsys, s, "drafted")
	mustCreate(t, life, s)

	for _, name := range filled(t, s) {
		content, err := fsys.Read(t.Context(), name)
		if err != nil {
			t.Fatalf("Read(%q) = %v", name, err)
		}
		if string(content) != "drafted" {
			t.Errorf("Read(%q) = %q, want %q", name, content, "drafted")
		}
	}
}

// The acceptance: a deleted workspace is gone from the files, the vectors, the
// graph and the keys alike, because all of them are one derived suffix under a
// different root.
func TestDeleteRemovesTheWorkspaceFromEveryArea(t *testing.T) {
	life, fsys := newLifecycle(t)
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	mustCreate(t, life, s)
	fill(t, fsys, s, "drafted")

	if err := life.Delete(t.Context(), s); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, name := range filled(t, s) {
		if _, err := fsys.Read(t.Context(), name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Read(%q) = %v, want fs.ErrNotExist", name, err)
		}
	}
	if left, err := life.List(t.Context(), "alice"); err != nil || len(left) != 0 {
		t.Errorf("List after delete = %v, %v, want no workspaces", left, err)
	}
}

// The isolation the constraint asks for: a delete reaches one subtree and no
// other, neither the same user's second workspace nor another user's workspace
// of the same name.
func TestDeleteLeavesEveryOtherScopeAlone(t *testing.T) {
	life, fsys := newLifecycle(t)
	deleted := workspace.Scope{User: "alice", Workspace: "cv"}
	kept := []workspace.Scope{
		{User: "alice", Workspace: "notes"},
		{User: "bob", Workspace: "cv"},
	}

	for _, s := range append([]workspace.Scope{deleted}, kept...) {
		mustCreate(t, life, s)
		fill(t, fsys, s, "drafted")
	}

	if err := life.Delete(t.Context(), deleted); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, s := range kept {
		for _, name := range filled(t, s) {
			if _, err := fsys.Read(t.Context(), name); err != nil {
				t.Errorf("Read(%q) after deleting %+v = %v, want it kept", name, deleted, err)
			}
		}
	}
}

// A delete that failed halfway is completed by running it again, which is only
// true if a workspace that is already gone is not an error.
func TestDeleteOfAWorkspaceThatIsNotThereSucceeds(t *testing.T) {
	life, _ := newLifecycle(t)

	if err := life.Delete(t.Context(), workspace.Scope{User: "alice", Workspace: "cv"}); err != nil {
		t.Errorf("Delete of a missing workspace = %v, want nil", err)
	}
}

// The area that refuses must not take the others with it. A read-only mount
// stands in for an area that is refusing: what matters is that the loop reached
// the rest and said which one it could not clear.
func TestDeleteClearsEveryAreaWhenOneRefuses(t *testing.T) {
	table := vfs.NewMountTable()
	mustMount(t, table, files, memfs.New())
	mustMount(t, table, "/info", synth.NewTree(map[string]synth.Entry{"version": synth.Fixed("1")}))

	// The refusing area comes first, so a delete that abandoned the rest on the
	// first failure would leave the files area holding everything it wrote.
	life, err := workspace.NewLifecycle(table, "/info", files)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	s := workspace.Scope{User: "alice", Workspace: "cv"}
	mustMkdirAt(t, table, "/files/alice/cv")
	mustWriteAt(t, table, "/files/alice/cv/entry", "drafted")

	err = life.Delete(t.Context(), s)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Delete = %v, want fs.ErrPermission", err)
	}
	if !strings.Contains(err.Error(), "/info/alice/cv") {
		t.Errorf("Delete = %v, want it to name the area it could not clear", err)
	}
	if _, err := table.Read(t.Context(), "/files/alice/cv/entry"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read after a partly refused delete = %v, want the area that answered to be cleared", err)
	}
}

func mustMkdirAt(t *testing.T, fsys vfs.FileSystem, name string) {
	t.Helper()
	if err := fsys.Mkdir(t.Context(), name); err != nil {
		t.Fatalf("Mkdir(%q): %v", name, err)
	}
}

func mustWriteAt(t *testing.T, fsys vfs.FileSystem, name, content string) {
	t.Helper()
	if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", name, err)
	}
}

// Deleting user scope would remove every workspace the user has under a verb
// named for one.
func TestCreateAndDeleteRefuseAScopeNamingNoWorkspace(t *testing.T) {
	life, _ := newLifecycle(t)
	s := workspace.Scope{User: "alice"}

	for name, verb := range map[string]func(context.Context, workspace.Scope) error{
		"Create": life.Create,
		"Delete": life.Delete,
	} {
		if err := verb(t.Context(), s); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("%s of user scope = %v, want fs.ErrInvalid", name, err)
		}
	}
}

// One name per workspace however many areas hold part of it, and none of
// another user's.
func TestListNamesTheUsersWorkspacesOnceEach(t *testing.T) {
	life, _ := newLifecycle(t)
	for _, s := range []workspace.Scope{
		{User: "alice", Workspace: "notes"},
		{User: "alice", Workspace: "cv"},
		{User: "bob", Workspace: "cv"},
	} {
		mustCreate(t, life, s)
	}

	got, err := life.List(t.Context(), "alice")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []string{"cv", "notes"}; !slices.Equal(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

// A key the user wrote in user scope sits beside their workspaces and is not
// one. The keyspace is what makes this reachable: it answers for a prefix with
// the directory bit set and for a key without it.
func TestListSkipsAKeyWrittenInUserScope(t *testing.T) {
	life, fsys := newLifecycle(t)
	mustCreate(t, life, workspace.Scope{User: "alice", Workspace: "cv"})
	mustWriteAt(t, fsys, "/kv/alice/cv/state", "open")
	mustWriteAt(t, fsys, "/kv/alice/token", "secret")

	got, err := life.List(t.Context(), "alice")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []string{"cv"}; !slices.Equal(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

// User scope comes into existence when something is put under it, so a user
// nothing was ever written for has no workspaces rather than an error.
func TestListOfAUserWithNothingIsEmpty(t *testing.T) {
	life, _ := newLifecycle(t)

	got, err := life.List(t.Context(), "ghost")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want no workspaces", got)
	}
}

func TestListRefusesAUserThatIsNotAName(t *testing.T) {
	life, _ := newLifecycle(t)

	if _, err := life.List(t.Context(), "../bob"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("List = %v, want fs.ErrInvalid", err)
	}
}

// A lifecycle over no areas would be a Delete that removes nothing and reports
// that it worked.
func TestNewLifecycleRefusesNoAreas(t *testing.T) {
	if _, err := workspace.NewLifecycle(vfs.NewMountTable()); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewLifecycle with no areas = %v, want fs.ErrInvalid", err)
	}
}

func TestNewLifecycleRefusesAnAreaThatIsNotARootedCleanPath(t *testing.T) {
	for _, area := range []string{"", "files", "/files/", "/files/../kv"} {
		if _, err := workspace.NewLifecycle(vfs.NewMountTable(), area); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewLifecycle(%q) = %v, want fs.ErrInvalid", area, err)
		}
	}
}
