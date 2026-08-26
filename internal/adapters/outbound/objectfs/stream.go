package objectfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsStreamer reports that an object can be moved in pieces. It is the one
// optional verb set an object store answers: a bucket hands back a body a
// caller reads as it arrives, which is what Read cannot do.
//
// Truncate and the link verbs are refused. Cutting an object to a length means
// fetching it, cutting it here and storing it back, which is a caller's
// read-modify-write dressed up as a verb; a bucket has no links at all.
func (f *FS) AsStreamer() vfs.Streamer { return f }

// OpenRead returns the content of name as a stream over the object as it stood
// when it opened. A write partway through cannot hand the reader two versions,
// because a stored object is replaced whole rather than edited.
func (f *FS) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	stream, _, err := f.open(ctx, name)
	if err != nil {
		return nil, err
	}
	return stream, nil
}

// OpenWrite replaces the content of name from a stream. Nothing is stored until
// Close, because an object is stored whole: there is no partial object for a
// caller that fails halfway to leave behind.
func (f *FS) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	if isRoot(name) {
		return nil, fmt.Errorf("open_write %q: the bucket itself: %w", name, fs.ErrInvalid)
	}
	return &pending{fsys: f, name: name, ctx: ctx}, nil
}

// pending collects a streamed write and stores it whole when it is closed.
//
// The context is held rather than passed because io.WriteCloser has no place
// for one: it is the context of the OpenWrite that made this writer, and the
// caller closes what it opened.
type pending struct {
	fsys   *FS
	name   string
	ctx    context.Context //nolint:containedctx // io.WriteCloser carries no context; this is OpenWrite's.
	buffer bytes.Buffer
	closed bool
}

func (p *pending) Write(data []byte) (int, error) {
	if p.closed {
		return 0, fmt.Errorf("write %q: the stream is closed: %w", p.name, fs.ErrClosed)
	}
	return p.buffer.Write(data)
}

// Close stores what was streamed. Closing twice stores nothing the second time,
// so a deferred Close after an explicit one is not a second write.
func (p *pending) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	return p.fsys.Write(p.ctx, p.name, p.buffer.Bytes())
}
