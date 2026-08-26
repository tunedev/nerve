package memfs_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

// The stream is over what was there when it opened, so a write partway through
// reading cannot hand the reader half of one version and half of another. The
// port does not promise this; a tree in memory can afford it.
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
