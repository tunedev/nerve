package vfs_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// stalling is a plugin whose transfers never move a byte: the stream it hands
// back waits for its transfer to end rather than answering. It is what a far
// end that goes mute halfway through a download looks like.
type stalling struct {
	stub
	vfs.NoAccessors
}

func (s stalling) AsStreamer() vfs.Streamer { return s }
func (s stalling) AsSubber() vfs.Subber     { return nil }

func (stalling) OpenRead(ctx context.Context, _ string) (io.ReadCloser, error) {
	return mute{ctx: ctx}, nil
}

func (stalling) OpenWrite(ctx context.Context, _ string) (io.WriteCloser, error) {
	return mute{ctx: ctx}, nil
}

// mute is one stalled transfer. The context is held rather than passed
// because a stream carries none: it is the context of the open that made it.
type mute struct {
	ctx context.Context //nolint:containedctx // a stream carries no context; this is its open's.
}

func (q mute) Read([]byte) (int, error)  { <-q.ctx.Done(); return 0, q.ctx.Err() }
func (q mute) Write([]byte) (int, error) { <-q.ctx.Done(); return 0, q.ctx.Err() }
func (mute) Close() error                { return nil }

// trickling is a plugin whose reader hands back one byte at a time with a gap
// between them, which is a download that is slow rather than stalled.
type trickling struct {
	stub
	vfs.NoAccessors
	bytes int
	gap   time.Duration
}

func (t trickling) AsStreamer() vfs.Streamer { return t }
func (t trickling) AsSubber() vfs.Subber     { return nil }

func (t trickling) OpenRead(context.Context, string) (io.ReadCloser, error) {
	return &trickle{left: t.bytes, gap: t.gap}, nil
}

func (t trickling) OpenWrite(context.Context, string) (io.WriteCloser, error) {
	return &trickle{left: t.bytes, gap: t.gap}, nil
}

// trickle is one slow transfer: every call moves one byte, gap after waiting.
type trickle struct {
	left int
	gap  time.Duration
}

func (t *trickle) Read(into []byte) (int, error) {
	if t.left == 0 {
		return 0, io.EOF
	}
	time.Sleep(t.gap)
	t.left--
	into[0] = 'x'
	return 1, nil
}

func (t *trickle) Write(data []byte) (int, error) {
	time.Sleep(t.gap)
	return len(data), nil
}

func (*trickle) Close() error { return nil }

// The whole ticket: a transfer that stops moving bytes is out of time, even
// though the open that returned it answered inside the bound.
func TestAStreamThatStopsMovingBytesFails(t *testing.T) {
	fsys := vfs.WithTimeout(stalling{}, 20*time.Millisecond)

	stream, err := fsys.AsStreamer().OpenRead(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer stream.Close()

	if _, err := io.ReadAll(stream); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("reading a stream that moves nothing = %v, want %v", err, context.DeadlineExceeded)
	}
}

// The bound is on the transfer and not on the direction: a write nobody is
// taking is as stuck as a read nobody is sending.
func TestAWriteThatStopsMovingBytesFails(t *testing.T) {
	fsys := vfs.WithTimeout(stalling{}, 20*time.Millisecond)

	stream, err := fsys.AsStreamer().OpenWrite(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("payload")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("writing to a stream that takes nothing = %v, want %v", err, context.DeadlineExceeded)
	}
}

// A write is complete exactly when Close returns nil, so a transfer the bound
// ended must not close as though it landed.
func TestClosingAStalledWriteReportsIt(t *testing.T) {
	fsys := vfs.WithTimeout(stalling{}, 20*time.Millisecond)

	stream, err := fsys.AsStreamer().OpenWrite(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}

	time.Sleep(60 * time.Millisecond)

	if err := stream.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("closing a stream the bound ended = %v, want %v", err, context.DeadlineExceeded)
	}
}

// The bound is on idleness and not on duration: a transfer that keeps moving
// bytes runs as long as it needs to, which is the download the ticket is
// careful not to cut.
func TestASlowButProgressingStreamIsNotCut(t *testing.T) {
	const bound = 100 * time.Millisecond
	slow := trickling{bytes: 10, gap: 20 * time.Millisecond}

	fsys := vfs.WithTimeout(slow, bound)

	stream, err := fsys.AsStreamer().OpenRead(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer stream.Close()

	content, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("reading a slow but progressing stream: %v", err)
	}
	if len(content) != slow.bytes {
		t.Errorf("read %d bytes, want %d", len(content), slow.bytes)
	}
}
