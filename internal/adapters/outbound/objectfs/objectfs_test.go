package objectfs_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The conformance suite asserts the port. What is left here is what an object
// store alone promises: a prefix is a directory only while something is under
// it, a name carries no subtree with it, and a mode kept beside the content
// survives the next write.

func mustWrite(t *testing.T, fsys *objectfs.FS, name, content string) {
	t.Helper()
	if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func mustStat(t *testing.T, fsys *objectfs.FS, name string) vfs.FileInfo {
	t.Helper()
	info, err := fsys.Stat(t.Context(), name)
	if err != nil {
		t.Fatalf("Stat(%q) = %v", name, err)
	}
	return info
}

// A prefix is a directory for exactly as long as something is stored under it.
// Nothing was made to bring it into being and nothing has to be removed to end
// it: emptying what is under it is what takes it away.
func TestAPrefixIsADirectoryOnlyWhileSomethingIsUnderIt(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/jobs/queued/first", "work")

	for _, name := range []string{"/jobs", "/jobs/queued"} {
		if info := mustStat(t, fsys, name); !info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = false, want true", name)
		}
	}

	if err := fsys.Remove(t.Context(), "/jobs/queued/first"); err != nil {
		t.Fatalf("Remove = %v", err)
	}
	for _, name := range []string{"/jobs", "/jobs/queued"} {
		if _, err := fsys.Stat(t.Context(), name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) after the last entry under it went = %v, want fs.ErrNotExist", name, err)
		}
	}
}

// Remove deletes an object. A prefix is not an object, so there is nothing
// under that name for Remove to take, and what is stored beneath stays.
func TestRemoveOfAPrefixDoesNotExist(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/jobs/queued", "work")

	if err := fsys.Remove(t.Context(), "/jobs"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove of a prefix = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Stat(t.Context(), "/jobs/queued"); err != nil {
		t.Errorf("Stat after a refused Remove = %v, want the entry", err)
	}
}

// RemoveAll is the verb that reaches under a prefix, and it reaches all the way
// down: a flat namespace has no level for it to stop at.
func TestRemoveAllReachesEveryKeyUnderThePrefix(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/jobs/queued/high/first", "work")
	mustWrite(t, fsys, "/jobs/audit", "log")
	mustWrite(t, fsys, "/kept", "elsewhere")

	if err := fsys.RemoveAll(t.Context(), "/jobs"); err != nil {
		t.Fatalf("RemoveAll = %v", err)
	}

	for _, name := range []string{"/jobs/queued/high/first", "/jobs/audit", "/jobs"} {
		if _, err := fsys.Stat(t.Context(), name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) after RemoveAll = %v, want fs.ErrNotExist", name, err)
		}
	}
	if _, err := fsys.Stat(t.Context(), "/kept"); err != nil {
		t.Errorf("Stat of an entry beside the prefix removed = %v, want the entry", err)
	}
}

// Rename moves one object. A name has no subtree to bring along, so what is
// stored under the old prefix is still under the old prefix afterwards — the
// one place an object store visibly parts company with a tree.
func TestRenameMovesOneObjectAndNotWhatIsUnderThePrefix(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/jobs", "the object")
	mustWrite(t, fsys, "/jobs/queued", "under the prefix")

	if err := fsys.Rename(t.Context(), "/jobs", "/done"); err != nil {
		t.Fatalf("Rename = %v", err)
	}

	if content, err := fsys.Read(t.Context(), "/done"); err != nil || string(content) != "the object" {
		t.Errorf("Read(%q) = %q, %v, want %q", "/done", content, err, "the object")
	}
	if content, err := fsys.Read(t.Context(), "/jobs/queued"); err != nil || string(content) != "under the prefix" {
		t.Errorf("Read(%q) = %q, %v, want it left where it was", "/jobs/queued", content, err)
	}
}

// The mode lives beside the content rather than in it, so the next write has to
// carry it forward. A write that reset it would make Chmod something a caller
// cannot rely on having done.
func TestWriteKeepsTheModeChmodSet(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/notes", "first")

	if err := fsys.Chmod(t.Context(), "/notes", 0o600); err != nil {
		t.Fatalf("Chmod = %v", err)
	}
	mustWrite(t, fsys, "/notes", "second")

	if perm := mustStat(t, fsys, "/notes").Mode.Perm(); perm != 0o600 {
		t.Errorf("Mode.Perm() after a write = %v, want %v", perm, fs.FileMode(0o600))
	}
}

// Chmod does not touch the content, and the copy that carries the new mode is
// the bucket's rather than a fetch and a store through this process.
func TestChmodLeavesTheContentAlone(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/notes", "kept")

	if err := fsys.Chmod(t.Context(), "/notes", 0o600); err != nil {
		t.Fatalf("Chmod = %v", err)
	}

	content, err := fsys.Read(t.Context(), "/notes")
	if err != nil || string(content) != "kept" {
		t.Errorf("Read after Chmod = %q, %v, want %q", content, err, "kept")
	}
}

// One level of a flat namespace is objects and the prefixes a delimiter stopped
// at, merged and sorted together: a caller listing a prefix cannot tell which
// of the two an entry came from.
func TestReadDirMergesObjectsAndPrefixes(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/jobs/queued/first", "deeper")
	mustWrite(t, fsys, "/jobs/config", "settings")
	mustWrite(t, fsys, "/jobs/audit", "log")

	entries, err := fsys.ReadDir(t.Context(), "/jobs")
	if err != nil {
		t.Fatalf("ReadDir = %v", err)
	}

	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	if want := []string{"audit", "config", "queued"}; !slices.Equal(listed, want) {
		t.Fatalf("ReadDir = %v, want %v", listed, want)
	}
	if entries[0].IsDir() || entries[0].Size != 3 {
		t.Errorf("audit = %+v, want size 3 and not a directory", entries[0])
	}
	if !entries[2].IsDir() {
		t.Errorf("queued = %+v, want a directory", entries[2])
	}
}

// A prefix with nothing under it lists nothing rather than failing: there was
// no directory there to be missing.
func TestReadDirOfAPrefixWithNothingUnderItListsNothing(t *testing.T) {
	fsys := objectfs.New(newMemBucket())

	entries, err := fsys.ReadDir(t.Context(), "/absent")
	if err != nil {
		t.Fatalf("ReadDir of a prefix with nothing under it = %v, want nil", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir = %v, want no entries", entries)
	}
}
