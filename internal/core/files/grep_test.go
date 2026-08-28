package files_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/core/files"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestGrepNamesTheFileTheLineAndTheNumber(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "stand-up\nsalary review\nlunch\n")

		found, err := tree.Grep(t.Context(), here, "/", "salary", 0)
		if err != nil {
			t.Fatalf("Grep: %v", err)
		}
		want := []files.Line{{Path: "notes/monday.md", Number: 2, Text: "salary review"}}
		if len(found) != 1 || found[0] != want[0] {
			t.Fatalf("Grep = %v, want %v", found, want)
		}
	})
}

// The expression is RE2, which is what makes it safe to take from whoever is
// asking: no pattern backtracks, so none makes the search run away.
func TestGrepTakesAnExpressionAndNotALiteral(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/cv.md", "salary: 90000\nnotice: 30 days\n")

		found, err := tree.Grep(t.Context(), here, "/", `^salary: [0-9]+$`, 0)
		if err != nil {
			t.Fatalf("Grep: %v", err)
		}
		if len(found) != 1 || found[0].Text != "salary: 90000" {
			t.Fatalf("Grep = %v, want the one salary line", found)
		}
	})
}

func TestGrepRefusesAnExpressionItCannotCompile(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, _ vfs.FileSystem) {
		if _, err := tree.Grep(t.Context(), here, "/", "(unclosed", 0); err == nil {
			t.Fatal("Grep of an expression that will not compile succeeded, want an error")
		}
	})
}

// The limit is what keeps one common word from returning the workspace.
func TestGrepStopsAtTheLimit(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/log.txt", "a\na\na\na\n")

		found, err := tree.Grep(t.Context(), here, "/", "a", 2)
		if err != nil {
			t.Fatalf("Grep: %v", err)
		}
		if len(found) != 2 {
			t.Fatalf("Grep with a limit of 2 returned %d lines, want 2", len(found))
		}
	})
}

// A line of a binary is not a line, and the caller asked for text.
func TestGrepPassesOverAFileHoldingANulByte(t *testing.T) {
	onBoth(t, func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem) {
		mustWrite(t, fsys, here, "/notes/monday.md", "salary review\n")
		mustWrite(t, fsys, here, "/notes/image.png", "salary\x00review\n")

		found, err := tree.Grep(t.Context(), here, "/", "salary", 0)
		if err != nil {
			t.Fatalf("Grep: %v", err)
		}
		if len(found) != 1 || found[0].Path != "notes/monday.md" {
			t.Fatalf("Grep = %v, want only the text file", found)
		}
	})
}
