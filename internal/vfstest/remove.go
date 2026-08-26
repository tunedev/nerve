package vfstest

import (
	"errors"
	"io/fs"
	"testing"
)

// removeCases are what Remove and RemoveAll promise, including the difference
// between them: Remove refuses a directory with entries in it, and RemoveAll is
// where deleting what is inside one is asked for on purpose.
func removeCases() []testCase {
	return []testCase{
		{name: "Remove deletes an entry", when: writable, run: removeDeletesAnEntry},
		{name: "Remove deletes an empty directory", when: needs(writable, tree), run: removeDeletesAnEmptyDirectory},
		{name: "Remove refuses a directory that is not empty", when: needs(writable, tree), run: removeRefusesANonEmptyDirectory},
		{name: "Remove of a missing path does not exist", when: writable, run: removeOfAMissingPath},
		{name: "Remove of the root is invalid", when: writable, run: removeOfTheRoot},
		{name: "RemoveAll deletes everything beneath", when: needs(writable, tree), run: removeAllDeletesEverythingBeneath},
		{name: "RemoveAll of a missing path is not an error", when: writable, run: removeAllOfAMissingPath},
		{name: "RemoveAll of the root empties it", when: writable, run: removeAllOfTheRootEmptiesIt},
	}
}

func removeDeletesAnEntry(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	if err := w.fsys.Remove(t.Context(), w.at("notes")); err != nil {
		t.Fatalf("Remove(%q) = %v", w.at("notes"), err)
	}

	if _, err := w.fsys.Stat(t.Context(), w.at("notes")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after Remove = %v, want fs.ErrNotExist", err)
	}
}

func removeDeletesAnEmptyDirectory(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")

	if err := w.fsys.Remove(t.Context(), w.at("jobs")); err != nil {
		t.Fatalf("Remove(%q) = %v", w.at("jobs"), err)
	}

	if _, err := w.fsys.Stat(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after Remove = %v, want fs.ErrNotExist", err)
	}
}

// A typo is not destructive: the entries under the directory are still there
// after the refusal.
func removeRefusesANonEmptyDirectory(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")
	mustWrite(t, w, "jobs/audit", "log")

	if err := w.fsys.Remove(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Remove of a directory with entries in it = %v, want fs.ErrExist", err)
	}
	if _, err := w.fsys.Stat(t.Context(), w.at("jobs/audit")); err != nil {
		t.Errorf("Stat after a refused Remove = %v, want the entry", err)
	}
}

func removeOfAMissingPath(t *testing.T, w world) {
	if err := w.fsys.Remove(t.Context(), w.at("absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove of a missing path = %v, want fs.ErrNotExist", err)
	}
}

// The root is the ground the plugin stands on, and no verb deletes it.
func removeOfTheRoot(t *testing.T, w world) {
	if err := w.fsys.Remove(t.Context(), w.root); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Remove(%q) = %v, want fs.ErrInvalid", w.root, err)
	}
}

func removeAllDeletesEverythingBeneath(t *testing.T, w world) {
	mustMkdir(t, w, "jobs/queued")
	mustWrite(t, w, "jobs/queued/first", "work")
	mustWrite(t, w, "kept", "elsewhere")

	if err := w.fsys.RemoveAll(t.Context(), w.at("jobs")); err != nil {
		t.Fatalf("RemoveAll(%q) = %v", w.at("jobs"), err)
	}

	if _, err := w.fsys.Stat(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after RemoveAll = %v, want fs.ErrNotExist", err)
	}
	if _, err := w.fsys.Stat(t.Context(), w.at("kept")); err != nil {
		t.Errorf("Stat of an entry beside the one removed = %v, want the entry", err)
	}
}

func removeAllOfAMissingPath(t *testing.T, w world) {
	if err := w.fsys.RemoveAll(t.Context(), w.at("absent")); err != nil {
		t.Errorf("RemoveAll of a missing path = %v, want nil", err)
	}
}

// The root cannot be deleted, so RemoveAll empties it instead: everything
// beneath the path is what the verb promises either way.
func removeAllOfTheRootEmptiesIt(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	if err := w.fsys.RemoveAll(t.Context(), w.root); err != nil {
		t.Fatalf("RemoveAll(%q) = %v", w.root, err)
	}

	entries, err := w.fsys.ReadDir(t.Context(), w.root)
	if err != nil {
		t.Fatalf("ReadDir(%q) after RemoveAll = %v", w.root, err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(%q) = %v, want no entries", w.root, names(entries))
	}
}
