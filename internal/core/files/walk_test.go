package files_test

import (
	"errors"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/files"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// sample is a workspace with enough shape to tell the three walks apart: a
// name that repeats at two depths, and a directory whose name sorts either side
// of a sibling file depending on whether paths or names are compared.
func sample(t *testing.T, fsys vfs.FileSystem) {
	t.Helper()
	mustWrite(t, fsys, here, "/cv.md", "ada lovelace")
	mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")
	mustWrite(t, fsys, here, "/notes/deep/monday.md", "again")
	mustWrite(t, fsys, here, "/notes.txt", "loose")
}

func TestWalkReturnsTheWholeSubtreeInTreeOrder(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		sample(t, fsys)

		found, err := tree.Walk(t.Context(), here, "/")
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		want := []string{"cv.md", "notes", "notes/deep", "notes/deep/monday.md", "notes/monday.md", "notes.txt"}
		if got := paths(found); !slices.Equal(got, want) {
			t.Fatalf("Walk = %v, want %v", got, want)
		}
	})
}

func TestWalkSaysWhichEntriesAreDirectories(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		sample(t, fsys)

		found, err := tree.Walk(t.Context(), here, "/notes")
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		dirs := map[string]bool{}
		for _, entry := range found {
			dirs[entry.Path] = entry.Info.IsDir()
		}
		if !dirs["deep"] || dirs["monday.md"] {
			t.Fatalf("Walk = %v, want deep a directory and monday.md not", dirs)
		}
	})
}

// Find asks where the file called this is, so the pattern meets the name and
// the depth does not matter.
func TestFindMatchesTheNameAtEveryDepth(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		sample(t, fsys)

		found, err := tree.Find(t.Context(), here, "/", "monday.md")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		want := []string{"notes/deep/monday.md", "notes/monday.md"}
		if got := paths(found); !slices.Equal(got, want) {
			t.Fatalf("Find = %v, want %v", got, want)
		}
	})
}

func TestFindStartsWhereItIsTold(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		sample(t, fsys)

		found, err := tree.Find(t.Context(), here, "/notes/deep", "*.md")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if got := paths(found); !slices.Equal(got, []string{"monday.md"}) {
			t.Fatalf("Find under /notes/deep = %v, want only monday.md", got)
		}
	})
}

// Glob asks what is at this shape of path, so "*" stops at a separator and a
// pattern one level deep says nothing about what is below it.
func TestGlobMatchesTheWholePathAndDoesNotCrossASeparator(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		sample(t, fsys)

		found, err := tree.Glob(t.Context(), here, "notes/*.md")
		if err != nil {
			t.Fatalf("Glob: %v", err)
		}
		if got := paths(found); !slices.Equal(got, []string{"notes/monday.md"}) {
			t.Fatalf("Glob = %v, want only notes/monday.md", got)
		}
	})
}

func TestGlobIsAnchoredAtTheWorkspaceRoot(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		sample(t, fsys)

		found, err := tree.Glob(t.Context(), here, "*.md")
		if err != nil {
			t.Fatalf("Glob: %v", err)
		}
		if got := paths(found); !slices.Equal(got, []string{"cv.md"}) {
			t.Fatalf("Glob = %v, want only the top-level cv.md", got)
		}
	})
}

// A pattern nobody can parse fails the call. Matched lazily it would answer
// "nothing found" over an empty workspace and "syntax error" over a full one,
// which is the same call telling a caller two different things.
func TestABadPatternFailsOverAnEmptyWorkspace(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, _ vfs.FileSystem) {
		if err := tree.Mkdir(t.Context(), here, "/"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if _, err := tree.Glob(t.Context(), here, "[unclosed"); !errors.Is(err, path.ErrBadPattern) {
			t.Fatalf("Glob of a bad pattern = %v, want path.ErrBadPattern", err)
		}
		if _, err := tree.Find(t.Context(), here, "/", "[unclosed"); !errors.Is(err, path.ErrBadPattern) {
			t.Fatalf("Find of a bad pattern = %v, want path.ErrBadPattern", err)
		}
	})
}
