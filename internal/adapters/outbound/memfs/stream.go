package memfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsStreamer reports that a tree in memory can be moved in pieces. Every entry
// is already a byte slice, so a stream over one is a view of what is there.
func (f *FS) AsStreamer() vfs.Streamer { return f }

// AsTruncater reports that an entry can be cut to a length in place.
func (f *FS) AsTruncater() vfs.Truncater { return f }

// Truncate sets the length of name's content, padding with zero bytes when size
// is past the end.
func (f *FS) Truncate(_ context.Context, name string, size int64) error {
	if size < 0 {
		return fmt.Errorf("truncate %q to %d: a length is not negative: %w", name, size, fs.ErrInvalid)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	found, err := f.walk(name)
	if err != nil {
		return fmt.Errorf("truncate %w", err)
	}
	if found.isDir() {
		return fmt.Errorf("truncate %q: a directory: %w", name, fs.ErrInvalid)
	}

	cut := make([]byte, size)
	copy(cut, found.data)
	found.data = cut
	found.modTime = time.Now()
	return nil
}

// OpenRead returns the content of name as a stream over what was there when it
// opened. A write partway through cannot hand the reader two versions.
func (f *FS) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	content, err := f.Read(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("open_read %q: %w", name, err)
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

// OpenWrite replaces the content of name from a stream. Nothing is stored until
// Close, so a caller that fails partway through leaves what was there.
//
// Whether the entry can be written at all is answered here rather than at
// Close, so a caller learns about a missing parent before it streams a payload
// into a buffer nothing will accept.
func (f *FS) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	f.mu.RLock()
	holder, base, err := f.parent(name)
	if err == nil {
		if existing, taken := holder.children[base]; taken && existing.isDir() {
			err = fmt.Errorf("%q: a directory: %w", name, fs.ErrInvalid)
		}
	}
	f.mu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("open_write %w", err)
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
