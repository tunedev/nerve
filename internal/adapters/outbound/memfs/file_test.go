package memfs_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

func TestCreateMakesAnEmptyFile(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	if err := fsys.Create(ctx, "/notes"); err != nil {
		t.Fatalf("Create(/notes) = %v", err)
	}

	content, err := fsys.Read(ctx, "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if len(content) != 0 {
		t.Errorf("Read(/notes) = %q, want empty", content)
	}
}

func TestCreateTruncatesWhatIsAlreadyThere(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "kept")

	if err := fsys.Create(ctx, "/notes"); err != nil {
		t.Fatalf("Create(/notes) = %v", err)
	}

	content, _ := fsys.Read(ctx, "/notes")
	if len(content) != 0 {
		t.Errorf("Read(/notes) = %q, want empty after Create", content)
	}
}

func TestWriteCreatesWhatIsNotThere(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	if err := fsys.Write(ctx, "/notes", []byte("first")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}

	content, err := fsys.Read(ctx, "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if string(content) != "first" {
		t.Errorf("Read(/notes) = %q, want %q", content, "first")
	}
}

func TestWriteReplacesTheWholeContent(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "a longer first version")

	if err := fsys.Write(ctx, "/notes", []byte("short")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}

	content, _ := fsys.Read(ctx, "/notes")
	if string(content) != "short" {
		t.Errorf("Read(/notes) = %q, want %q", content, "short")
	}
}

// The caller holds a slice the tree does not, so writing through it later
// cannot reach in and change what is stored.
func TestWriteCopiesWhatItIsGiven(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	given := []byte("stored")
	if err := fsys.Write(ctx, "/notes", given); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}
	given[0] = 'X'

	content, _ := fsys.Read(ctx, "/notes")
	if string(content) != "stored" {
		t.Errorf("Read(/notes) = %q, want %q", content, "stored")
	}
}

// Read hands back a copy for the same reason Write takes one.
func TestReadCopiesWhatItHandsBack(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "stored")

	content, _ := fsys.Read(ctx, "/notes")
	content[0] = 'X'

	again, _ := fsys.Read(ctx, "/notes")
	if string(again) != "stored" {
		t.Errorf("Read(/notes) = %q, want %q", again, "stored")
	}
}

func TestReadOfAMissingPathIsNotExist(t *testing.T) {
	_, err := memfs.New().Read(context.Background(), "/absent")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/absent) = %v, want fs.ErrNotExist", err)
	}
}

func TestReadOfADirectoryIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	_, err := fsys.Read(ctx, "/jobs")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Read(/jobs) = %v, want fs.ErrInvalid", err)
	}
}

func TestWriteUnderAMissingParentIsNotExist(t *testing.T) {
	err := memfs.New().Write(context.Background(), "/absent/notes", []byte("x"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Write(/absent/notes) = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteOverADirectoryIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	err := fsys.Write(ctx, "/jobs", []byte("x"))
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Write(/jobs) = %v, want fs.ErrInvalid", err)
	}
}

func mustWrite(t *testing.T, fsys *memfs.FS, name, content string) {
	t.Helper()
	if err := fsys.Write(context.Background(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func mustMkdir(t *testing.T, fsys *memfs.FS, name string) {
	t.Helper()
	if err := fsys.Mkdir(context.Background(), name); err != nil {
		t.Fatalf("Mkdir(%q) = %v", name, err)
	}
}

func TestCreateOverADirectoryIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	if err := fsys.Create(ctx, "/jobs"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Create(/jobs) = %v, want fs.ErrInvalid", err)
	}
}

func TestCreateUnderAMissingParentIsNotExist(t *testing.T) {
	if err := memfs.New().Create(context.Background(), "/absent/notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Create(/absent/notes) = %v, want fs.ErrNotExist", err)
	}
}

// Stat reports when the content last changed, so a caller that caches an entry
// can tell that it has to read again.
func TestWriteMovesModTimeForward(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "first")

	before, _ := fsys.Stat(ctx, "/notes")
	mustWrite(t, fsys, "/notes", "second")
	after, _ := fsys.Stat(ctx, "/notes")

	if !after.ModTime.After(before.ModTime) {
		t.Errorf("ModTime after a write = %v, want later than %v", after.ModTime, before.ModTime)
	}
}
