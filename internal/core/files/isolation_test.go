package files_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/files"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// escapes are the spellings a caller inside one workspace would reach another
// with if the package derived paths itself and forgot one. Every one of them
// lands on the workspace root instead.
var escapes = []string{
	"../../grace/hunt/secret",
	"/../notes/secret",
	"../../../../etc/passwd",
}

// A workspace holds someone's CV, employment history and salary expectations.
// Nothing spelled inside one may name another, and the verbs that take two
// paths are where that is easiest to get wrong.
func TestNoPathSpelledInsideOneWorkspaceReachesAnother(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, other, "/secret", "grace's salary expectations")
		mustWrite(t, fsys, there, "/secret", "ada's other workspace")
		mustWrite(t, fsys, here, "/cv.md", "ada lovelace")

		for _, escape := range escapes {
			if _, err := tree.Stat(t.Context(), here, escape); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Stat(%q) = %v, want fs.ErrNotExist", escape, err)
			}
			if err := tree.Rename(t.Context(), here, "/cv.md", escape); err == nil {
				t.Errorf("Rename onto %q succeeded, want it contained", escape)
			}
			if err := tree.Copy(t.Context(), here, escape, "/stolen"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Copy from %q = %v, want fs.ErrNotExist", escape, err)
			}
			if err := tree.DeleteAll(t.Context(), here, escape); err != nil {
				t.Errorf("DeleteAll(%q) = %v, want it to land inside the workspace", escape, err)
			}
		}

		if got := mustRead(t, fsys, other, "/secret"); got != "grace's salary expectations" {
			t.Fatalf("grace's file after the escapes = %q, want it untouched", got)
		}
		if got := mustRead(t, fsys, there, "/secret"); got != "ada's other workspace" {
			t.Fatalf("ada's other workspace after the escapes = %q, want it untouched", got)
		}
	})
}

// A walk never crosses out of the workspace it started in, whatever else the
// mount holds.
func TestNoWalkInsideOneWorkspaceNamesAnother(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, other, "/secret", "grace's salary expectations")
		mustWrite(t, fsys, here, "/cv.md", "ada lovelace")

		found, err := tree.Walk(t.Context(), here, "/")
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		for _, entry := range found {
			if entry.Path != "cv.md" {
				t.Errorf("Walk found %q, want only ada's own files", entry.Path)
			}
		}
		lines, err := tree.Grep(t.Context(), here, "/", "salary", 0)
		if err != nil {
			t.Fatalf("Grep: %v", err)
		}
		if len(lines) != 0 {
			t.Fatalf("Grep found %v, want nothing of grace's", lines)
		}
	})
}

// The symlink target is the path nothing beneath the core dispatches: it is
// clean, inside one mount and inside the plugin's root, so every layer under
// the view permits it. The view derives it, so there is no spelling for it.
func TestALinkMadeInOneWorkspaceCannotReachAnother(t *testing.T) {
	tree, fsys := newTree(t, plugins["on disk"])
	mustWrite(t, fsys, other, "/secret", "grace's salary expectations")
	if err := tree.Mkdir(t.Context(), here, "/"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if err := tree.Symlink(t.Context(), here, "../../grace/hunt/secret", "/peek"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := inside(t, fsys, here).Read(t.Context(), "/peek"); err == nil {
		t.Fatal("reading through the link returned something, want nothing of grace's reachable")
	}
	if got := mustRead(t, fsys, other, "/secret"); got != "grace's salary expectations" {
		t.Fatalf("grace's file = %q, want it untouched", got)
	}
}

func TestALinkStandsForWhatItWasGivenInsideTheWorkspace(t *testing.T) {
	tree, fsys := newTree(t, plugins["on disk"])
	mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")

	if err := tree.Symlink(t.Context(), here, "/notes/monday.md", "/today.md"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if got := mustRead(t, fsys, here, "/today.md"); got != "stand-up" {
		t.Fatalf("reading through the link = %q, want %q", got, "stand-up")
	}
}

// A mount with no links says so through the accessor. A type assertion would
// interrogate the mount table and never the plugin that would do the work.
func TestSymlinkIsRefusedOnAMountWithNoLinks(t *testing.T) {
	tree, _ := newTree(t, plugins["in memory"])
	if err := tree.Mkdir(t.Context(), here, "/"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	err := tree.Symlink(t.Context(), here, "/notes/monday.md", "/today.md")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Symlink on a mount with no links = %v, want fs.ErrInvalid", err)
	}
}

// One user's workspaces are as separate as two users' are: the scope is the
// boundary, and a workspace name is half of it.
func TestOneUsersWorkspacesDoNotSeeEachOther(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, there, "/secret", "ada's other workspace")

		found, err := tree.Find(t.Context(), here, "/", "secret")
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if len(found) != 0 {
			t.Fatalf("Find = %v, want nothing from the other workspace", paths(found))
		}
	})
}

func TestAScopeThatIsNotAFlatNameIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, _ vfs.FileSystem) {
		for _, bad := range []workspace.Scope{
			{User: "ada/../grace", Workspace: "hunt"},
			{User: "ada", Workspace: ".."},
			{User: "", Workspace: "hunt"},
		} {
			if _, err := tree.List(t.Context(), bad, "/"); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("List at %+v = %v, want fs.ErrInvalid", bad, err)
			}
		}
	})
}
