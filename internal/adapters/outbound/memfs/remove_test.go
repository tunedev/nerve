package memfs_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

func TestRemoveDeletesAFile(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "text")

	if err := fsys.Remove(ctx, "/notes"); err != nil {
		t.Fatalf("Remove(/notes) = %v", err)
	}

	if _, err := fsys.Stat(ctx, "/notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(/notes) after Remove = %v, want fs.ErrNotExist", err)
	}
}

func TestRemoveDeletesAnEmptyDirectory(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	if err := fsys.Remove(ctx, "/jobs"); err != nil {
		t.Fatalf("Remove(/jobs) = %v", err)
	}

	if _, err := fsys.Stat(ctx, "/jobs"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(/jobs) after Remove = %v, want fs.ErrNotExist", err)
	}
}

func TestRemoveRefusesADirectoryThatIsNotEmpty(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")
	mustWrite(t, fsys, "/jobs/audit", "log")

	if err := fsys.Remove(ctx, "/jobs"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Remove(/jobs) = %v, want fs.ErrExist", err)
	}
	if _, err := fsys.Stat(ctx, "/jobs/audit"); err != nil {
		t.Errorf("Stat(/jobs/audit) after a refused Remove = %v, want the entry", err)
	}
}

func TestRemoveOfAMissingPathIsNotExist(t *testing.T) {
	if err := memfs.New().Remove(context.Background(), "/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(/absent) = %v, want fs.ErrNotExist", err)
	}
}

func TestRemoveOfTheRootIsInvalid(t *testing.T) {
	if err := memfs.New().Remove(context.Background(), "/"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Remove(/) = %v, want fs.ErrInvalid", err)
	}
}

func TestRemoveAllDeletesEverythingBeneath(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs/queued")
	mustWrite(t, fsys, "/jobs/queued/first", "work")
	mustWrite(t, fsys, "/kept", "elsewhere")

	if err := fsys.RemoveAll(ctx, "/jobs"); err != nil {
		t.Fatalf("RemoveAll(/jobs) = %v", err)
	}

	if _, err := fsys.Stat(ctx, "/jobs"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(/jobs) after RemoveAll = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Stat(ctx, "/kept"); err != nil {
		t.Errorf("Stat(/kept) after RemoveAll(/jobs) = %v, want the entry", err)
	}
}

func TestRemoveAllOfAMissingPathIsNotAnError(t *testing.T) {
	if err := memfs.New().RemoveAll(context.Background(), "/absent"); err != nil {
		t.Errorf("RemoveAll(/absent) = %v, want nil", err)
	}
}

// The root is the one path RemoveAll cannot delete, so it empties it instead.
// Everything beneath it is what the verb promises, and for the root that is the
// whole tree.
func TestRemoveAllOfTheRootEmptiesTheTree(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs/queued")
	mustWrite(t, fsys, "/notes", "text")

	if err := fsys.RemoveAll(ctx, "/"); err != nil {
		t.Fatalf("RemoveAll(/) = %v", err)
	}

	entries, err := fsys.ReadDir(ctx, "/")
	if err != nil {
		t.Fatalf("ReadDir(/) after RemoveAll = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/) = %v, want no entries", entries)
	}
}
