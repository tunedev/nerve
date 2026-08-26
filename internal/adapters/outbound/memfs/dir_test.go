package memfs_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

func TestMkdirMakesMissingParents(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	if err := fsys.Mkdir(ctx, "/jobs/queued/high"); err != nil {
		t.Fatalf("Mkdir(/jobs/queued/high) = %v", err)
	}

	for _, name := range []string{"/jobs", "/jobs/queued", "/jobs/queued/high"} {
		info, err := fsys.Stat(ctx, name)
		if err != nil {
			t.Fatalf("Stat(%q) = %v, want a directory", name, err)
		}
		if !info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = false, want true", name)
		}
	}
}

func TestMkdirOfADirectoryAlreadyThereIsNotAnError(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	if err := fsys.Mkdir(ctx, "/jobs"); err != nil {
		t.Errorf("Mkdir(/jobs) twice = %v, want nil", err)
	}
}

func TestMkdirWhereAFileIsIsExist(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/jobs", "a file")

	if err := fsys.Mkdir(ctx, "/jobs/queued"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Mkdir(/jobs/queued) = %v, want fs.ErrExist", err)
	}
}

func TestReadDirListsDirectChildrenSortedByName(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs/queued")
	mustWrite(t, fsys, "/jobs/config", "settings")
	mustWrite(t, fsys, "/jobs/audit", "log")
	mustWrite(t, fsys, "/jobs/queued/first", "deeper")

	entries, err := fsys.ReadDir(ctx, "/jobs")
	if err != nil {
		t.Fatalf("ReadDir(/jobs) = %v", err)
	}

	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	want := []string{"audit", "config", "queued"}
	if !slices.Equal(names, want) {
		t.Errorf("ReadDir(/jobs) = %v, want %v", names, want)
	}
}

func TestReadDirOfAFileIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "text")

	if _, err := fsys.ReadDir(ctx, "/notes"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadDir(/notes) = %v, want fs.ErrInvalid", err)
	}
}

func TestReadDirReportsSizeAndDirectoryBit(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs/queued")
	mustWrite(t, fsys, "/jobs/audit", "12345")

	entries, err := fsys.ReadDir(ctx, "/jobs")
	if err != nil {
		t.Fatalf("ReadDir(/jobs) = %v", err)
	}

	if entries[0].Size != 5 || entries[0].IsDir() {
		t.Errorf("audit = %+v, want size 5 and not a directory", entries[0])
	}
	if entries[1].Size != 0 || !entries[1].IsDir() {
		t.Errorf("queued = %+v, want size 0 and a directory", entries[1])
	}
}

// A path arrives however the caller spelled it. memfs resolves it lexically, so
// the caller is not made to clean paths a mount table would have cleaned.
func TestPathsResolveLexically(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")
	mustWrite(t, fsys, "/jobs/audit", "log")

	for _, name := range []string{"jobs/audit", "/jobs//audit", "/jobs/./audit", "/jobs/queued/../audit"} {
		if _, err := fsys.Stat(ctx, name); err != nil {
			t.Errorf("Stat(%q) = %v, want the entry at /jobs/audit", name, err)
		}
	}
}
