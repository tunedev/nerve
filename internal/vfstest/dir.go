package vfstest

import (
	"errors"
	"io/fs"
	"slices"
	"testing"
)

// dirCases are what Mkdir, ReadDir and Stat promise about a tree and the
// entries in it.
func dirCases() []testCase {
	return []testCase{
		{name: "A new filesystem holds nothing", when: needs(writable, tree), run: newFilesystemHoldsNothing},
		{name: "The root is a directory", when: tree, run: rootIsADirectory},
		{name: "Mkdir makes missing parents", when: needs(writable, tree), run: mkdirMakesMissingParents},
		{name: "Mkdir of a directory already there is not an error", when: needs(writable, tree), run: mkdirTwiceIsNotAnError},
		{name: "Mkdir where a file is exists", when: needs(writable, tree), run: mkdirWhereAFileIs},
		{name: "ReadDir lists direct children sorted by name", when: needs(writable, tree), run: readDirListsSortedChildren},
		{name: "ReadDir reports size and the directory bit", when: needs(writable, tree), run: readDirReportsSizeAndKind},
		{name: "ReadDir of a file is invalid", when: needs(writable, tree), run: readDirOfAFile},
		{name: "Stat describes an entry", when: writable, run: statDescribesAnEntry},
		{name: "Stat of a missing path does not exist", run: statOfAMissingPath},
		{name: "Paths resolve lexically", when: needs(writable, tree), run: pathsResolveLexically},
	}
}

// Only a mount the suite can write to started empty for the suite's benefit. A
// read-only one holds what its plugin computes, and holding nothing would make
// it a mount with nothing to say.
func newFilesystemHoldsNothing(t *testing.T, w world) {
	entries, err := w.fsys.ReadDir(t.Context(), w.root)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", w.root, err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(%q) = %v, want no entries", w.root, names(entries))
	}
}

func rootIsADirectory(t *testing.T, w world) {
	info, err := w.fsys.Stat(t.Context(), w.root)
	if err != nil {
		t.Fatalf("Stat(%q) = %v, want a directory", w.root, err)
	}
	if !info.IsDir() {
		t.Errorf("Stat(%q).IsDir() = false, want true", w.root)
	}
}

func mkdirMakesMissingParents(t *testing.T, w world) {
	mustMkdir(t, w, "jobs/queued/high")

	for _, name := range []string{"jobs", "jobs/queued", "jobs/queued/high"} {
		info, err := w.fsys.Stat(t.Context(), w.at(name))
		if err != nil {
			t.Fatalf("Stat(%q) = %v, want a directory", w.at(name), err)
		}
		if !info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = false, want true", w.at(name))
		}
	}
}

func mkdirTwiceIsNotAnError(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")

	if err := w.fsys.Mkdir(t.Context(), w.at("jobs")); err != nil {
		t.Errorf("Mkdir twice = %v, want nil", err)
	}
}

func mkdirWhereAFileIs(t *testing.T, w world) {
	mustWrite(t, w, "jobs", "a file")

	if err := w.fsys.Mkdir(t.Context(), w.at("jobs/queued")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Mkdir under a file = %v, want fs.ErrExist", err)
	}
}

func readDirListsSortedChildren(t *testing.T, w world) {
	mustMkdir(t, w, "jobs/queued")
	mustWrite(t, w, "jobs/config", "settings")
	mustWrite(t, w, "jobs/audit", "log")
	mustWrite(t, w, "jobs/queued/first", "deeper")

	entries, err := w.fsys.ReadDir(t.Context(), w.at("jobs"))
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", w.at("jobs"), err)
	}

	want := []string{"audit", "config", "queued"}
	if listed := names(entries); !slices.Equal(listed, want) {
		t.Errorf("ReadDir(%q) = %v, want %v", w.at("jobs"), listed, want)
	}
}

func readDirReportsSizeAndKind(t *testing.T, w world) {
	mustMkdir(t, w, "jobs/queued")
	mustWrite(t, w, "jobs/audit", "12345")

	entries, err := w.fsys.ReadDir(t.Context(), w.at("jobs"))
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", w.at("jobs"), err)
	}
	if len(entries) != 2 {
		t.Fatalf("ReadDir(%q) = %v, want two entries", w.at("jobs"), names(entries))
	}

	if entries[0].Size != 5 || entries[0].IsDir() {
		t.Errorf("audit = %+v, want size 5 and not a directory", entries[0])
	}
	if entries[1].Size != 0 || !entries[1].IsDir() {
		t.Errorf("queued = %+v, want size 0 and a directory", entries[1])
	}
}

func readDirOfAFile(t *testing.T, w world) {
	mustWrite(t, w, "notes", "text")

	if _, err := w.fsys.ReadDir(t.Context(), w.at("notes")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadDir of a file = %v, want fs.ErrInvalid", err)
	}
}

func statDescribesAnEntry(t *testing.T, w world) {
	mustWrite(t, w, "notes", "12345")

	info, err := w.fsys.Stat(t.Context(), w.at("notes"))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", w.at("notes"), err)
	}
	if info.Name != "notes" {
		t.Errorf("Stat(%q).Name = %q, want %q", w.at("notes"), info.Name, "notes")
	}
	if info.Size != 5 {
		t.Errorf("Stat(%q).Size = %d, want 5", w.at("notes"), info.Size)
	}
	if info.IsDir() {
		t.Errorf("Stat(%q).IsDir() = true, want false", w.at("notes"))
	}
}

func statOfAMissingPath(t *testing.T, w world) {
	if _, err := w.fsys.Stat(t.Context(), w.at("absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of a missing path = %v, want fs.ErrNotExist", err)
	}
}

// A path arrives however the caller spelled it, and a plugin resolves it
// lexically rather than making the caller clean what a mount table has cleaned.
// The spellings are joined by hand: path.Join would clean them first, which is
// the work being asserted.
func pathsResolveLexically(t *testing.T, w world) {
	mustMkdir(t, w, "jobs")
	mustWrite(t, w, "jobs/audit", "log")

	spellings := []string{w.root + "//jobs/audit", w.root + "/jobs/./audit", w.root + "/jobs/queued/../audit"}
	if w.root == "/" {
		spellings = append(spellings, "jobs/audit")
	}

	for _, name := range spellings {
		if _, err := w.fsys.Stat(t.Context(), name); err != nil {
			t.Errorf("Stat(%q) = %v, want the entry at %q", name, err, w.at("jobs/audit"))
		}
	}
}
