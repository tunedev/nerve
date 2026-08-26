package vfstest

import (
	"errors"
	"io/fs"
	"testing"
)

// fileCases are what Create, Read and Write promise about one entry.
func fileCases() []testCase {
	return []testCase{
		{name: "Create makes an empty entry", when: writable, run: createMakesAnEmptyEntry},
		{name: "Create truncates what is already there", when: needs(writable, replacing), run: createTruncates},
		{name: "Create over a directory is invalid", when: needs(writable, tree), run: createOverADirectory},
		{name: "Create under a missing parent does not exist", when: needs(writable, tree), run: createUnderAMissingParent},
		{name: "Write creates what is not there", when: writable, run: writeCreatesWhatIsNotThere},
		{name: "Write replaces the whole content", when: needs(writable, replacing), run: writeReplacesTheContent},
		{name: "Write copies what it is given", when: writable, run: writeCopiesWhatItIsGiven},
		{name: "Write under a missing parent does not exist", when: needs(writable, tree), run: writeUnderAMissingParent},
		{name: "Write over a directory is invalid", when: needs(writable, tree), run: writeOverADirectory},
		{name: "Write moves ModTime forward", when: writable, run: writeMovesModTimeForward},
		{name: "Read copies what it hands back", when: needs(writable, repeatable), run: readCopiesWhatItHandsBack},
		{name: "Read of a missing path does not exist", run: readOfAMissingPath},
		{name: "Read of a directory is invalid", when: needs(writable, tree), run: readOfADirectory},
	}
}

func createMakesAnEmptyEntry(t *testing.T, w world) {
	if err := w.fsys.Create(t.Context(), w.at("notes")); err != nil {
		t.Fatalf("Create(%q) = %v", w.at("notes"), err)
	}

	if content := mustRead(t, w, "notes"); content != "" {
		t.Errorf("Read after Create = %q, want empty", content)
	}
}

func createTruncates(t *testing.T, w world) {
	mustWrite(t, w, "notes", "kept")

	if err := w.fsys.Create(t.Context(), w.at("notes")); err != nil {
		t.Fatalf("Create(%q) = %v", w.at("notes"), err)
	}

	if content := mustRead(t, w, "notes"); content != "" {
		t.Errorf("Read after Create = %q, want empty", content)
	}
}

func createOverADirectory(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")

	if err := w.fsys.Create(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Create over a directory = %v, want fs.ErrInvalid", err)
	}
}

func createUnderAMissingParent(t *testing.T, w world) {
	if err := w.fsys.Create(t.Context(), w.at("absent/notes")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Create under a missing parent = %v, want fs.ErrNotExist", err)
	}
}

func writeCreatesWhatIsNotThere(t *testing.T, w world) {
	mustWrite(t, w, "notes", "first")

	if content := mustRead(t, w, "notes"); content != "first" {
		t.Errorf("Read after Write = %q, want %q", content, "first")
	}
}

func writeReplacesTheContent(t *testing.T, w world) {
	mustWrite(t, w, "notes", "a longer first version")
	mustWrite(t, w, "notes", "short")

	if content := mustRead(t, w, "notes"); content != "short" {
		t.Errorf("Read after a second Write = %q, want %q", content, "short")
	}
}

// The caller holds a slice the plugin does not, so writing through it later
// cannot reach in and change what is stored.
func writeCopiesWhatItIsGiven(t *testing.T, w world) {
	given := []byte("stored")
	if err := w.fsys.Write(t.Context(), w.at("notes"), given); err != nil {
		t.Fatalf("Write(%q) = %v", w.at("notes"), err)
	}
	given[0] = 'X'

	if content := mustRead(t, w, "notes"); content != "stored" {
		t.Errorf("Read = %q, want %q", content, "stored")
	}
}

func writeUnderAMissingParent(t *testing.T, w world) {
	err := w.fsys.Write(t.Context(), w.at("absent/notes"), []byte("x"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Write under a missing parent = %v, want fs.ErrNotExist", err)
	}
}

func writeOverADirectory(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")

	if err := w.fsys.Write(t.Context(), w.at("jobs"), []byte("x")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Write over a directory = %v, want fs.ErrInvalid", err)
	}
}

// Stat reports when the content last changed, so a caller holding a cached
// entry can tell that it has to read again.
func writeMovesModTimeForward(t *testing.T, w world) {
	mustWrite(t, w, "notes", "first")
	before, err := w.fsys.Stat(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", w.at("notes"), err)
	}

	mustWrite(t, w, "notes", "second")
	after, err := w.fsys.Stat(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", w.at("notes"), err)
	}

	if !after.ModTime.After(before.ModTime) {
		t.Errorf("ModTime after a write = %v, want later than %v", after.ModTime, before.ModTime)
	}
}

// Read hands back a copy for the same reason Write takes one.
func readCopiesWhatItHandsBack(t *testing.T, w world) {
	mustWrite(t, w, "notes", "stored")

	content, err := w.fsys.Read(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("Read(%q) = %v", w.at("notes"), err)
	}
	content[0] = 'X'

	if again := mustRead(t, w, "notes"); again != "stored" {
		t.Errorf("Read = %q, want %q", again, "stored")
	}
}

func readOfAMissingPath(t *testing.T, w world) {
	if _, err := w.fsys.Read(t.Context(), w.at("absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func readOfADirectory(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")

	if _, err := w.fsys.Read(t.Context(), w.at("jobs")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Read of a directory = %v, want fs.ErrInvalid", err)
	}
}
