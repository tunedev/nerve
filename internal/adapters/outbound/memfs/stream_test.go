package memfs_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

func TestTruncateShortensContent(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "abcdef")

	if err := fsys.AsTruncater().Truncate(ctx, "/notes", 3); err != nil {
		t.Fatalf("Truncate(/notes, 3) = %v", err)
	}

	content, _ := fsys.Read(ctx, "/notes")
	if string(content) != "abc" {
		t.Errorf("Read(/notes) = %q, want %q", content, "abc")
	}
}

func TestTruncatePadsWithZeroBytes(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "ab")

	if err := fsys.AsTruncater().Truncate(ctx, "/notes", 4); err != nil {
		t.Fatalf("Truncate(/notes, 4) = %v", err)
	}

	content, _ := fsys.Read(ctx, "/notes")
	if string(content) != "ab\x00\x00" {
		t.Errorf("Read(/notes) = %q, want %q", content, "ab\x00\x00")
	}
}

func TestTruncateToANegativeSizeIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "abc")

	if err := fsys.AsTruncater().Truncate(ctx, "/notes", -1); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Truncate(/notes, -1) = %v, want fs.ErrInvalid", err)
	}
}

func TestTruncateOfADirectoryIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	if err := fsys.AsTruncater().Truncate(ctx, "/jobs", 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Truncate(/jobs, 0) = %v, want fs.ErrInvalid", err)
	}
}

func TestTruncateOfAMissingPathIsNotExist(t *testing.T) {
	err := memfs.New().AsTruncater().Truncate(context.Background(), "/absent", 0)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Truncate(/absent, 0) = %v, want fs.ErrNotExist", err)
	}
}

func TestOpenReadStreamsTheContent(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "streamed")

	stream, err := fsys.AsStreamer().OpenRead(ctx, "/notes")
	if err != nil {
		t.Fatalf("OpenRead(/notes) = %v", err)
	}
	defer stream.Close()

	content, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("ReadAll(/notes) = %v", err)
	}
	if string(content) != "streamed" {
		t.Errorf("ReadAll(/notes) = %q, want %q", content, "streamed")
	}
}

// The stream is over what was there when it opened, so a write partway through
// reading cannot hand the reader half of one version and half of another.
func TestOpenReadIsUnaffectedByALaterWrite(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "original")

	stream, err := fsys.AsStreamer().OpenRead(ctx, "/notes")
	if err != nil {
		t.Fatalf("OpenRead(/notes) = %v", err)
	}
	defer stream.Close()
	mustWrite(t, fsys, "/notes", "replaced")

	content, _ := io.ReadAll(stream)
	if string(content) != "original" {
		t.Errorf("ReadAll(/notes) = %q, want %q", content, "original")
	}
}

func TestOpenReadOfAMissingPathIsNotExist(t *testing.T) {
	_, err := memfs.New().AsStreamer().OpenRead(context.Background(), "/absent")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenRead(/absent) = %v, want fs.ErrNotExist", err)
	}
}

func TestOpenReadOfADirectoryIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	_, err := fsys.AsStreamer().OpenRead(ctx, "/jobs")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("OpenRead(/jobs) = %v, want fs.ErrInvalid", err)
	}
}

func TestOpenWriteReplacesTheContentOnClose(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustWrite(t, fsys, "/notes", "original")

	stream, err := fsys.AsStreamer().OpenWrite(ctx, "/notes")
	if err != nil {
		t.Fatalf("OpenWrite(/notes) = %v", err)
	}
	if _, err := io.WriteString(stream, "replaced"); err != nil {
		t.Fatalf("Write to /notes = %v", err)
	}

	before, _ := fsys.Read(ctx, "/notes")
	if string(before) != "original" {
		t.Errorf("Read(/notes) before Close = %q, want %q", before, "original")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close(/notes) = %v", err)
	}
	after, _ := fsys.Read(ctx, "/notes")
	if string(after) != "replaced" {
		t.Errorf("Read(/notes) after Close = %q, want %q", after, "replaced")
	}
}

func TestOpenWriteCreatesWhatIsNotThere(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	stream, err := fsys.AsStreamer().OpenWrite(ctx, "/notes")
	if err != nil {
		t.Fatalf("OpenWrite(/notes) = %v", err)
	}
	if _, err := io.WriteString(stream, "first"); err != nil {
		t.Fatalf("Write to /notes = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close(/notes) = %v", err)
	}

	content, err := fsys.Read(ctx, "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if string(content) != "first" {
		t.Errorf("Read(/notes) = %q, want %q", content, "first")
	}
}

func TestOpenWriteUnderAMissingParentIsNotExist(t *testing.T) {
	_, err := memfs.New().AsStreamer().OpenWrite(context.Background(), "/absent/notes")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenWrite(/absent/notes) = %v, want fs.ErrNotExist", err)
	}
}

func TestOpenWriteOverADirectoryIsInvalid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	_, err := fsys.AsStreamer().OpenWrite(ctx, "/jobs")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("OpenWrite(/jobs) = %v, want fs.ErrInvalid", err)
	}
}

// The caller's `if s := fsys.AsStreamer(); s != nil` guard has to find the verb
// sets memfs answers, or nothing reaches them.
func TestMemfsAnswersTheStreamAndTruncateVerbs(t *testing.T) {
	fsys := memfs.New()

	if stream := fsys.AsStreamer(); stream == nil {
		t.Error("AsStreamer() = nil, want the plugin")
	}
	if cut := fsys.AsTruncater(); cut == nil {
		t.Error("AsTruncater() = nil, want the plugin")
	}
}

// A deferred Close after an explicit one is the shape every caller writes, so
// the second must not store the buffer again over a later write.
func TestClosingAWriteStreamTwiceStoresItOnce(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	stream, err := fsys.AsStreamer().OpenWrite(ctx, "/notes")
	if err != nil {
		t.Fatalf("OpenWrite(/notes) = %v", err)
	}
	io.WriteString(stream, "streamed")
	if err := stream.Close(); err != nil {
		t.Fatalf("Close(/notes) = %v", err)
	}
	mustWrite(t, fsys, "/notes", "later")

	if err := stream.Close(); err != nil {
		t.Errorf("Close(/notes) twice = %v, want nil", err)
	}
	content, _ := fsys.Read(ctx, "/notes")
	if string(content) != "later" {
		t.Errorf("Read(/notes) = %q, want %q", content, "later")
	}
}

func TestWritingToAClosedStreamIsClosed(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()

	stream, err := fsys.AsStreamer().OpenWrite(ctx, "/notes")
	if err != nil {
		t.Fatalf("OpenWrite(/notes) = %v", err)
	}
	stream.Close()

	if _, err := io.WriteString(stream, "too late"); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Write after Close = %v, want fs.ErrClosed", err)
	}
}
