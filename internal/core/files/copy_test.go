package files_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/files"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestCopyDuplicatesAFile(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/cv.md", "ada lovelace")

		if err := tree.Copy(t.Context(), here, "/cv.md", "/archive/cv.md"); err != nil {
			t.Fatalf("Copy: %v", err)
		}
		if got := mustRead(t, fsys, here, "/archive/cv.md"); got != "ada lovelace" {
			t.Fatalf("the copy holds %q, want %q", got, "ada lovelace")
		}
		if got := mustRead(t, fsys, here, "/cv.md"); got != "ada lovelace" {
			t.Fatalf("the source holds %q, want it untouched", got)
		}
	})
}

func TestCopyDuplicatesASubtreeIncludingItsEmptyDirectories(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/deep/monday.md", "stand-up")
		if err := tree.Mkdir(t.Context(), here, "/notes/empty"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}

		if err := tree.Copy(t.Context(), here, "/notes", "/archive"); err != nil {
			t.Fatalf("Copy: %v", err)
		}
		found, err := tree.Walk(t.Context(), here, "/archive")
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		want := []string{"deep", "deep/monday.md", "empty"}
		if got := paths(found); !slices.Equal(got, want) {
			t.Fatalf("the copied tree is %v, want %v", got, want)
		}
	})
}

// Writing one tree over another leaves a directory holding both, and neither of
// them is what the caller asked to have a copy of.
func TestCopyRefusesADestinationAlreadyThere(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/cv.md", "ada lovelace")
		mustWrite(t, fsys, here, "/archive/cv.md", "an older one")

		if err := tree.Copy(t.Context(), here, "/cv.md", "/archive/cv.md"); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("Copy onto an occupied path = %v, want fs.ErrExist", err)
		}
		if got := mustRead(t, fsys, here, "/archive/cv.md"); got != "an older one" {
			t.Fatalf("the destination holds %q, want it untouched", got)
		}
	})
}

func TestCopyRefusesAPathOntoItself(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/cv.md", "ada lovelace")

		if err := tree.Copy(t.Context(), here, "/cv.md", "cv.md"); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Copy onto itself = %v, want fs.ErrInvalid", err)
		}
	})
}

func TestCopyReportsASourceThatIsNotThere(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, _ vfs.FileSystem) {
		if err := tree.Copy(t.Context(), here, "/nothing", "/archive"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Copy of a missing source = %v, want fs.ErrNotExist", err)
		}
	})
}

// The walk finishes before the first write, so a destination inside the source
// copies the source as it stood when the call began rather than finding itself.
func TestCopyIntoTheSourceCopiesWhatWasThereWhenItBegan(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up")

		if err := tree.Copy(t.Context(), here, "/notes", "/notes/backup"); err != nil {
			t.Fatalf("Copy: %v", err)
		}
		found, err := tree.Walk(t.Context(), here, "/notes/backup")
		if err != nil {
			t.Fatalf("Walk: %v", err)
		}
		if got := paths(found); !slices.Equal(got, []string{"monday.md"}) {
			t.Fatalf("the copy holds %v, want only monday.md", got)
		}
	})
}
