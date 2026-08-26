package memfs_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

func TestRenameMovesAnEntryAndRenamesIt(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/archive")
	mustWrite(t, fsys, "/notes", "text")

	if err := fsys.Rename(ctx, "/notes", "/archive/old-notes"); err != nil {
		t.Fatalf("Rename(/notes, /archive/old-notes) = %v", err)
	}

	if _, err := fsys.Stat(ctx, "/notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(/notes) after Rename = %v, want fs.ErrNotExist", err)
	}
	info, err := fsys.Stat(ctx, "/archive/old-notes")
	if err != nil {
		t.Fatalf("Stat(/archive/old-notes) = %v", err)
	}
	if info.Name != "old-notes" {
		t.Errorf("Stat(/archive/old-notes).Name = %q, want %q", info.Name, "old-notes")
	}

	content, _ := fsys.Read(ctx, "/archive/old-notes")
	if string(content) != "text" {
		t.Errorf("Read(/archive/old-notes) = %q, want %q", content, "text")
	}
}

func TestRenameMovesAWholeSubtree(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs/queued")
	mustWrite(t, fsys, "/jobs/queued/first", "work")

	if err := fsys.Rename(ctx, "/jobs", "/done"); err != nil {
		t.Fatalf("Rename(/jobs, /done) = %v", err)
	}

	content, err := fsys.Read(ctx, "/done/queued/first")
	if err != nil {
		t.Fatalf("Read(/done/queued/first) = %v", err)
	}
	if string(content) != "work" {
		t.Errorf("Read(/done/queued/first) = %q, want %q", content, "work")
	}
}

func TestRenameReplacesTheEntryItLandsOn(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/next", "new")
	mustWrite(t, fsys, "/current", "old")

	if err := fsys.Rename(ctx, "/next", "/current"); err != nil {
		t.Fatalf("Rename(/next, /current) = %v", err)
	}

	content, _ := fsys.Read(ctx, "/current")
	if string(content) != "new" {
		t.Errorf("Read(/current) = %q, want %q", content, "new")
	}
}

func TestRenameRefusesToReplaceADirectoryThatIsNotEmpty(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/next", "new")
	mustMkdir(t, fsys, "/current")
	mustWrite(t, fsys, "/current/kept", "work")

	if err := fsys.Rename(ctx, "/next", "/current"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Rename(/next, /current) = %v, want fs.ErrExist", err)
	}
	if _, err := fsys.Stat(ctx, "/current/kept"); err != nil {
		t.Errorf("Stat(/current/kept) after a refused Rename = %v, want the entry", err)
	}
}

func TestRenameOfAMissingPathIsNotExist(t *testing.T) {
	err := memfs.New().Rename(context.Background(), "/absent", "/elsewhere")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename(/absent, /elsewhere) = %v, want fs.ErrNotExist", err)
	}
}

func TestRenameUnderAMissingParentIsNotExist(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "text")

	err := fsys.Rename(ctx, "/notes", "/absent/notes")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename(/notes, /absent/notes) = %v, want fs.ErrNotExist", err)
	}
}

// Moving a directory inside itself would cut the subtree loose from the root,
// leaving entries that no path reaches.
func TestRenameOfADirectoryIntoItselfIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs/queued")

	if err := fsys.Rename(ctx, "/jobs", "/jobs/queued/jobs"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Rename(/jobs, /jobs/queued/jobs) = %v, want fs.ErrInvalid", err)
	}
	if _, err := fsys.Stat(ctx, "/jobs/queued"); err != nil {
		t.Errorf("Stat(/jobs/queued) after a refused Rename = %v, want the entry", err)
	}
}

func TestRenameOfTheRootIsInvalid(t *testing.T) {
	if err := memfs.New().Rename(context.Background(), "/", "/elsewhere"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename(/, /elsewhere) = %v, want fs.ErrInvalid", err)
	}
}

func TestChmodSetsThePermissionBits(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "text")

	if err := fsys.Chmod(ctx, "/notes", 0o600); err != nil {
		t.Fatalf("Chmod(/notes, 0600) = %v", err)
	}

	info, _ := fsys.Stat(ctx, "/notes")
	if info.Mode.Perm() != 0o600 {
		t.Errorf("Stat(/notes).Mode.Perm() = %v, want %v", info.Mode.Perm(), fs.FileMode(0o600))
	}
}

// Mode carries the directory bit as well as the permission bits, and only the
// permission bits are Chmod's to set.
func TestChmodLeavesTheDirectoryBitAlone(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	if err := fsys.Chmod(ctx, "/jobs", 0o700); err != nil {
		t.Fatalf("Chmod(/jobs, 0700) = %v", err)
	}

	info, _ := fsys.Stat(ctx, "/jobs")
	if !info.IsDir() {
		t.Errorf("Stat(/jobs).IsDir() = false after Chmod, want true")
	}
	if info.Mode.Perm() != 0o700 {
		t.Errorf("Stat(/jobs).Mode.Perm() = %v, want %v", info.Mode.Perm(), fs.FileMode(0o700))
	}
}

func TestChmodOfAMissingPathIsNotExist(t *testing.T) {
	if err := memfs.New().Chmod(context.Background(), "/absent", 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Chmod(/absent) = %v, want fs.ErrNotExist", err)
	}
}

// Renaming an entry onto itself is the one move that changes nothing, and
// refusing it would make a caller that recomputes a path check first.
func TestRenameOntoItselfChangesNothing(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "text")

	if err := fsys.Rename(ctx, "/notes", "/notes"); err != nil {
		t.Fatalf("Rename(/notes, /notes) = %v", err)
	}

	content, err := fsys.Read(ctx, "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/notes) = %q, want %q", content, "text")
	}
}
