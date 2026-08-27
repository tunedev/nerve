package streamfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsStreamer reports that a stream can be moved in pieces, which is the whole
// of what a stream mount is for: Read hands back the window as it stands, and
// this is how a caller receives what arrives next.
func (f *FS) AsStreamer() vfs.Streamer { return f }

// OpenRead follows the stream at name. It hands back the bytes the ring still
// holds and then waits for what is appended, so a reader observes writes rather
// than reading the stream again to find them.
//
// It ends when the caller's context is done, or when the mount's follow time
// passes with nothing appended. A mount that follows for no time at all is a
// reader that ends as soon as it has caught up.
func (f *FS) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	stream, err := f.find(name)
	if err != nil {
		return nil, fmt.Errorf("open_read %w", err)
	}
	return &follower{stream: stream, name: name, at: stream.oldest(), follow: f.follow, ctx: ctx}, nil
}

// OpenWrite appends what is streamed to name, making the stream if it is not
// there.
//
// Each piece is appended as it arrives rather than held until Close, because a
// reader following the stream is what the mount exists for and buffering would
// keep the bytes from it. Close adds nothing: what was written is already in
// the ring.
func (f *FS) OpenWrite(_ context.Context, name string) (io.WriteCloser, error) {
	stream, err := f.open(name)
	if err != nil {
		return nil, fmt.Errorf("open_write %w", err)
	}
	return &appender{stream: stream, name: name}, nil
}

// follower is one reader's position in a stream.
//
// The context is held rather than passed because io.ReadCloser has no place for
// one: it is the context of the OpenRead that made this reader, and the caller
// closes what it opened.
type follower struct {
	stream *ring
	name   string
	at     int64
	follow time.Duration
	ctx    context.Context //nolint:containedctx // io.ReadCloser carries no context; this is OpenRead's.

	pending []byte
	closed  bool
}

// Read hands back the next bytes of the stream, waiting for them when the
// reader has caught up with what has been written.
func (r *follower) Read(into []byte) (int, error) {
	for {
		if len(r.pending) > 0 {
			taken := copy(into, r.pending)
			r.pending = r.pending[taken:]
			return taken, nil
		}
		if r.closed {
			return 0, fmt.Errorf("read %q: the stream is closed: %w", r.name, fs.ErrClosed)
		}

		chunk, next, appended, err := r.stream.since(r.at)
		if err != nil {
			return 0, fmt.Errorf("read %q: %w", r.name, err)
		}
		if len(chunk) > 0 {
			r.pending, r.at = chunk, next
			continue
		}
		if err := r.wait(appended); err != nil {
			return 0, err
		}
	}
}

// wait blocks until the stream is appended to. It ends the read when the
// caller's context is done or the mount's follow time passes first, which is
// what keeps a reader of a stream nobody is writing to from waiting forever.
func (r *follower) wait(appended <-chan struct{}) error {
	if r.follow == 0 {
		return io.EOF
	}

	idle := time.NewTimer(r.follow)
	defer idle.Stop()

	select {
	case <-appended:
		return nil
	case <-idle.C:
		return io.EOF
	case <-r.ctx.Done():
		return fmt.Errorf("read %q: %w", r.name, r.ctx.Err())
	}
}

// Close ends the reader. Closing twice is not an error, so a deferred Close
// after an explicit one is not a second one.
func (r *follower) Close() error {
	r.closed = true
	return nil
}

// appender is one writer's hold on a stream. It keeps no buffer: what it is
// given is appended before Write returns.
type appender struct {
	stream *ring
	name   string
	closed bool
}

func (w *appender) Write(data []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("write %q: the stream is closed: %w", w.name, fs.ErrClosed)
	}
	w.stream.append(data)
	return len(data), nil
}

// Close ends the writer. Nothing is stored here because nothing was held back,
// and closing twice is not an error.
func (w *appender) Close() error {
	w.closed = true
	return nil
}
