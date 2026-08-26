package vfstest

import (
	"errors"
	"io/fs"
	"testing"
)

// renameCases are what Rename and Chmod promise. They are together because
// Chmod is the only verb that sets a mode and Rename is the only one that moves
// an entry: between them they are what changes an entry without writing it.
func renameCases() []testCase {
	return []testCase{
		{name: "Rename moves an entry", when: writable, run: renameMovesAnEntry},
		{name: "Rename moves a whole subtree", when: needs(writable, tree), run: renameMovesASubtree},
		{name: "Rename replaces the entry it lands on", when: writable, run: renameReplacesTheLanding},
		{name: "Rename refuses to replace a directory that is not empty", when: needs(writable, tree), run: renameRefusesANonEmptyLanding},
		{name: "Rename onto itself changes nothing", when: writable, run: renameOntoItself},
		{name: "Rename of a missing path does not exist", when: writable, run: renameOfAMissingPath},
		{name: "Rename under a missing parent does not exist", when: needs(writable, tree), run: renameUnderAMissingParent},
		{name: "Rename of a directory into itself is invalid", when: needs(writable, tree), run: renameIntoItself},
		{name: "Rename of the root is invalid", when: writable, run: renameOfTheRoot},
		{name: "Chmod sets the permission bits", when: writable, run: chmodSetsThePermissionBits},
		{name: "Chmod leaves the directory bit alone", when: needs(writable, tree), run: chmodLeavesTheDirectoryBit},
		{name: "Chmod of a missing path does not exist", when: writable, run: chmodOfAMissingPath},
	}
}

func renameMovesAnEntry(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	if err := w.fsys.Rename(t.Context(), w.at("notes"), w.at("old-notes")); err != nil {
		t.Fatalf("Rename(%q, %q) = %v", w.at("notes"), w.at("old-notes"), err)
	}

	if _, err := w.fsys.Stat(t.Context(), w.at("notes")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of the old path = %v, want fs.ErrNotExist", err)
	}
	info, err := w.fsys.Stat(t.Context(), w.at("old-notes"))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", w.at("old-notes"), err)
	}
	if info.Name != "old-notes" {
		t.Errorf("Stat(%q).Name = %q, want %q", w.at("old-notes"), info.Name, "old-notes")
	}
	if content := mustRead(t, w, "old-notes"); content != "text" {
		t.Errorf("Read after Rename = %q, want %q", content, "text")
	}
}

func renameMovesASubtree(t *testing.T, w world) {
	mustMkdir(t, w, "jobs/queued")
	mustWrite(t, w, "jobs/queued/first", "work")

	if err := w.fsys.Rename(t.Context(), w.at("jobs"), w.at("done")); err != nil {
		t.Fatalf("Rename(%q, %q) = %v", w.at("jobs"), w.at("done"), err)
	}

	if content := mustRead(t, w, "done/queued/first"); content != "work" {
		t.Errorf("Read after Rename = %q, want %q", content, "work")
	}
}

func renameReplacesTheLanding(t *testing.T, w world) {
	mustWrite(t, w, "next", "new")
	mustWrite(t, w, "current", "old")

	if err := w.fsys.Rename(t.Context(), w.at("next"), w.at("current")); err != nil {
		t.Fatalf("Rename(%q, %q) = %v", w.at("next"), w.at("current"), err)
	}

	if content := mustRead(t, w, "current"); content != "new" {
		t.Errorf("Read of the landing = %q, want %q", content, "new")
	}
}

// Replacing a directory with entries in it is refused for the reason Remove
// refuses to delete one.
func renameRefusesANonEmptyLanding(t *testing.T, w world) {
	mustWrite(t, w, "next", "new")
	mustMkdir(t, w, "current")
	mustWrite(t, w, "current/kept", "work")

	if err := w.fsys.Rename(t.Context(), w.at("next"), w.at("current")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Rename onto a directory with entries in it = %v, want fs.ErrExist", err)
	}
	if _, err := w.fsys.Stat(t.Context(), w.at("current/kept")); err != nil {
		t.Errorf("Stat after a refused Rename = %v, want the entry", err)
	}
}

// The one move that changes nothing. Refusing it would make a caller that
// recomputes a path check whether it has to call at all.
func renameOntoItself(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	if err := w.fsys.Rename(t.Context(), w.at("notes"), w.at("notes")); err != nil {
		t.Fatalf("Rename onto itself = %v, want nil", err)
	}

	if content := mustRead(t, w, "notes"); content != "text" {
		t.Errorf("Read after Rename onto itself = %q, want %q", content, "text")
	}
}

func renameOfAMissingPath(t *testing.T, w world) {
	err := w.fsys.Rename(t.Context(), w.at("absent"), w.at("elsewhere"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func renameUnderAMissingParent(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	err := w.fsys.Rename(t.Context(), w.at("notes"), w.at("absent/notes"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename under a missing parent = %v, want fs.ErrNotExist", err)
	}
}

// Moving a directory inside itself would cut the subtree loose, leaving entries
// that no path reaches.
func renameIntoItself(t *testing.T, w world) {
	mustMkdir(t, w, "jobs/queued")

	err := w.fsys.Rename(t.Context(), w.at("jobs"), w.at("jobs/queued/jobs"))
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Rename of a directory into itself = %v, want fs.ErrInvalid", err)
	}
	if _, err := w.fsys.Stat(t.Context(), w.at("jobs/queued")); err != nil {
		t.Errorf("Stat after a refused Rename = %v, want the entry", err)
	}
}

func renameOfTheRoot(t *testing.T, w world) {
	if err := w.fsys.Rename(t.Context(), w.root, w.at("elsewhere")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename(%q) = %v, want fs.ErrInvalid", w.root, err)
	}
}

func chmodSetsThePermissionBits(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	if err := w.fsys.Chmod(t.Context(), w.at("notes"), 0o600); err != nil {
		t.Fatalf("Chmod(%q, 0600) = %v", w.at("notes"), err)
	}

	info, err := w.fsys.Stat(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", w.at("notes"), err)
	}
	if info.Mode.Perm() != 0o600 {
		t.Errorf("Mode.Perm() after Chmod = %v, want %v", info.Mode.Perm(), fs.FileMode(0o600))
	}
}

// Mode carries the directory bit as well as the permission bits, and only the
// permission bits are Chmod's to set.
func chmodLeavesTheDirectoryBit(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")

	if err := w.fsys.Chmod(t.Context(), w.at("jobs"), 0o700); err != nil {
		t.Fatalf("Chmod(%q, 0700) = %v", w.at("jobs"), err)
	}

	info, err := w.fsys.Stat(t.Context(), w.at("jobs"))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", w.at("jobs"), err)
	}
	if !info.IsDir() {
		t.Errorf("IsDir() after Chmod = false, want true")
	}
	if info.Mode.Perm() != 0o700 {
		t.Errorf("Mode.Perm() after Chmod = %v, want %v", info.Mode.Perm(), fs.FileMode(0o700))
	}
}

func chmodOfAMissingPath(t *testing.T, w world) {
	if err := w.fsys.Chmod(t.Context(), w.at("absent"), 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Chmod of a missing path = %v, want fs.ErrNotExist", err)
	}
}
