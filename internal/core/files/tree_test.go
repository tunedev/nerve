package files_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/files"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

func TestNewTreeRefusesARootThatIsNotARootedCleanPath(t *testing.T) {
	for _, bad := range []string{"files", "/files/", "/files/../files", ""} {
		if _, err := files.NewTree(vfs.NewMountTable(), bad); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewTree(%q) = %v, want fs.ErrInvalid", bad, err)
		}
	}
}

func TestListNamesTheChildrenOfADirectory(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")
		mustWrite(t, fsys, here, "/notes/tuesday.md", "review")

		entries, err := tree.List(t.Context(), here, "/notes")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
		if want := []string{"monday.md", "tuesday.md"}; !slices.Equal(names, want) {
			t.Fatalf("List = %v, want %v", names, want)
		}
	})
}

func TestStatDescribesAnEntryWithoutReadingIt(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")

		info, err := tree.Stat(t.Context(), here, "/notes/monday.md")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Name != "monday.md" || info.IsDir() || info.Size != int64(len("stand-up")) {
			t.Fatalf("Stat = %+v, want the file monday.md of 8 bytes", info)
		}
	})
}

func TestMkdirMakesTheDirectoriesOnTheWay(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, _ vfs.FileSystem) {
		if err := tree.Mkdir(t.Context(), here, "/a/b/c"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		info, err := tree.Stat(t.Context(), here, "/a/b")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("Stat(%q) = %+v, want a directory", "/a/b", info)
		}
	})
}

func TestRenameMovesAnEntryWithinTheWorkspace(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/draft.md", "first pass")
		if err := tree.Mkdir(t.Context(), here, "/final"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}

		if err := tree.Rename(t.Context(), here, "/draft.md", "/final/cv.md"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if got := mustRead(t, fsys, here, "/final/cv.md"); got != "first pass" {
			t.Fatalf("Read after Rename = %q, want %q", got, "first pass")
		}
		if _, err := tree.Stat(t.Context(), here, "/draft.md"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Stat of the old path = %v, want fs.ErrNotExist", err)
		}
	})
}

// The two deletes differ in blast radius, and that is why there are two. A
// caller that meant the subtree says so; one that did not gets an error rather
// than a subtree that is gone.
func TestDeleteRefusesADirectoryHoldingSomething(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")

		if err := tree.Delete(t.Context(), here, "/notes"); err == nil {
			t.Fatal("Delete of a directory holding a file succeeded, want an error")
		}
		if got := mustRead(t, fsys, here, "/notes/monday.md"); got != "stand-up" {
			t.Fatalf("the file after the refused Delete = %q, want it untouched", got)
		}
	})
}

func TestDeleteRemovesAFile(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")

		if err := tree.Delete(t.Context(), here, "/notes/monday.md"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := tree.Stat(t.Context(), here, "/notes/monday.md"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Stat after Delete = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestDeleteAllTakesTheSubtreeAndIsRepeatable(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/deep/monday.md", "stand-up")

		if err := tree.DeleteAll(t.Context(), here, "/notes"); err != nil {
			t.Fatalf("DeleteAll: %v", err)
		}
		if _, err := tree.Stat(t.Context(), here, "/notes"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Stat after DeleteAll = %v, want fs.ErrNotExist", err)
		}
		if err := tree.DeleteAll(t.Context(), here, "/notes"); err != nil {
			t.Fatalf("DeleteAll again: %v", err)
		}
	})
}

// A workspace exists or does not by the lifecycle's word. Emptying every file
// in one is not the same act as deleting it, and the view is what keeps them
// apart.
func TestDeleteAllOfTheRootEmptiesTheWorkspaceAndLeavesIt(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")

		if err := tree.DeleteAll(t.Context(), here, "/"); err != nil {
			t.Fatalf("DeleteAll: %v", err)
		}
		entries, err := tree.List(t.Context(), here, "/")
		if err != nil {
			t.Fatalf("List after DeleteAll of the root: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("List = %v, want the workspace empty and still there", entries)
		}
	})
}

// Every verb refuses a scope naming no workspace. A view over user scope has
// one workspace's files standing beside another's, so a walk would list across
// them and DeleteAll of "/" would empty all of them.
func TestEveryVerbRefusesAScopeNamingNoWorkspace(t *testing.T) {
	user := workspace.Scope{User: "ada"}
	onBoth(t, func(t *testing.T, tree *files.Tree, _ vfs.FileSystem) {
		verbs := map[string]func() error{
			"List":      func() error { _, err := tree.List(t.Context(), user, "/"); return err },
			"Stat":      func() error { _, err := tree.Stat(t.Context(), user, "/"); return err },
			"Walk":      func() error { _, err := tree.Walk(t.Context(), user, "/"); return err },
			"Find":      func() error { _, err := tree.Find(t.Context(), user, "/", "*"); return err },
			"Glob":      func() error { _, err := tree.Glob(t.Context(), user, "*"); return err },
			"Grep":      func() error { _, err := tree.Grep(t.Context(), user, "/", ".", 0); return err },
			"Mkdir":     func() error { return tree.Mkdir(t.Context(), user, "/a") },
			"Copy":      func() error { return tree.Copy(t.Context(), user, "/a", "/b") },
			"Rename":    func() error { return tree.Rename(t.Context(), user, "/a", "/b") },
			"Delete":    func() error { return tree.Delete(t.Context(), user, "/a") },
			"DeleteAll": func() error { return tree.DeleteAll(t.Context(), user, "/a") },
			"Symlink":   func() error { return tree.Symlink(t.Context(), user, "/a", "/b") },
		}
		for name, call := range verbs {
			if err := call(); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s at user scope = %v, want fs.ErrInvalid", name, err)
			}
		}
	})
}
