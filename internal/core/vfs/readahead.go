package vfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
)

// DefaultReadahead is how far in front of a caller a streaming read runs when
// nothing says otherwise. Eight fetches is enough for the fetch of the next
// piece to be under way while the caller works on the piece it has, and small
// enough that a caller which stops after one read has thrown away a quarter of
// a megabyte rather than a file.
const DefaultReadahead = 256 * 1024

// fetch is how much of a stream one pull moves. It is the buffer io.Copy uses,
// so a caller copying out of a stream that reads ahead takes whole pulls rather
// than parts of them.
const fetch = 32 * 1024

// WithReadahead returns fsys with a fetch of its own running in front of every
// streaming read: a goroutine pulls from the plugin into a queue holding up to
// window bytes, and the caller's Read is served from that queue.
//
// It is speculative. The bytes in the queue are bytes nobody has asked for, and
// a caller that stops early paid for at most window of them. What it buys is
// the overlap: the next piece is being fetched while the caller is working on
// the one it has, so a stream whose source costs latency per piece is consumed
// in the time of the slower of the two rather than in the sum of both.
//
// window is bytes. Zero or less returns fsys as it stands rather than a wrapper
// that reads ahead by nothing, which is how the whole thing is turned off.
//
// A path a read consumes is not read ahead of. What the queue holds when the
// caller closes is dropped, and dropping a piece of a queue's job would lose
// work nobody ever received; the mount says which paths those are, so nothing
// here has to know what a queue is.
//
// It goes under the bound rather than over it. Over it, the idle bound counts
// the plugin's reads instead of the caller's, and a caller slower than the
// queue is deep would have its transfer ended for a far end that was answering
// perfectly well.
func WithReadahead(fsys FileSystem, window int) FileSystem {
	if window <= 0 {
		return fsys
	}
	return readingAhead{fsys: fsys, window: window}
}

// readingAhead is one plugin with a fetch in front of its reads. Every verb but
// the streaming read is the plugin's own.
type readingAhead struct {
	fsys   FileSystem
	window int
}

func (r readingAhead) Create(ctx context.Context, path string) error {
	return r.fsys.Create(ctx, path)
}

func (r readingAhead) Mkdir(ctx context.Context, path string) error {
	return r.fsys.Mkdir(ctx, path)
}

func (r readingAhead) Remove(ctx context.Context, path string) error {
	return r.fsys.Remove(ctx, path)
}

func (r readingAhead) RemoveAll(ctx context.Context, path string) error {
	return r.fsys.RemoveAll(ctx, path)
}

func (r readingAhead) Read(ctx context.Context, path string) ([]byte, error) {
	return r.fsys.Read(ctx, path)
}

func (r readingAhead) Write(ctx context.Context, path string, data []byte) error {
	return r.fsys.Write(ctx, path, data)
}

func (r readingAhead) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	return r.fsys.ReadDir(ctx, path)
}

func (r readingAhead) Stat(ctx context.Context, path string) (FileInfo, error) {
	return r.fsys.Stat(ctx, path)
}

func (r readingAhead) Rename(ctx context.Context, oldPath, newPath string) error {
	return r.fsys.Rename(ctx, oldPath, newPath)
}

func (r readingAhead) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	return r.fsys.Chmod(ctx, path, mode)
}

// Capabilities is the plugin's answer. Reading ahead changes how bytes arrive
// and nothing about what a verb means at a path.
func (r readingAhead) Capabilities(path string) Caps { return r.fsys.Capabilities(path) }

// AsSymlinker is the plugin's link verbs, which have nothing to read ahead of.
func (r readingAhead) AsSymlinker() Symlinker { return r.fsys.AsSymlinker() }

// AsTruncater is the plugin's truncate verb, for the same reason.
func (r readingAhead) AsTruncater() Truncater { return r.fsys.AsTruncater() }

// AsStreamer returns the plugin's streaming verbs with a fetch in front of the
// read, or nil if it has none. Reading ahead says nothing about what a plugin
// can do, so a plugin that refused the streaming verbs still refuses them.
func (r readingAhead) AsStreamer() Streamer {
	stream := r.fsys.AsStreamer()
	if stream == nil {
		return nil
	}
	return aheadStreamer{stream: stream, caps: r.fsys.Capabilities, window: r.window}
}

// AsSubber returns the plugin's subtrees reading ahead by the same window, or
// nil if it has none.
func (r readingAhead) AsSubber() Subber {
	return SubOf(r.fsys, func(beneath FileSystem) FileSystem { return readingAhead{fsys: beneath, window: r.window} })
}

// aheadStreamer is the plugin's streaming verbs with the read pulled ahead. The
// write is the plugin's own: a caller streaming bytes in has them already, and
// there is nothing in front to fetch.
type aheadStreamer struct {
	stream Streamer
	caps   func(string) Caps
	window int
}

func (s aheadStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	source, err := s.stream.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	if s.caps(path).ReadDestructive {
		return source, nil
	}
	return readAhead(source, s.window), nil
}

func (s aheadStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	return s.stream.OpenWrite(ctx, path)
}

// pulled is one fetch off the source: what it moved, or why it stopped. Never
// both — a read that moved bytes and then failed sends the bytes first, so the
// caller receives everything that crossed before it is told the stream ended.
type pulled struct {
	data []byte
	err  error
}

// ahead is one read transfer with a fetch running in front of it.
//
// It is a stream like any other and is used by one goroutine at a time, which
// is what every io.ReadCloser here promises. The queue is the only thing two
// goroutines touch.
type ahead struct {
	source io.ReadCloser
	queue  chan pulled
	holds  []byte
	failed error
	closed bool
}

// readAhead starts the fetch and returns the stream it fills. window is bytes
// and is rounded down to whole fetches, never below one: a queue that can hold
// nothing turns every pull back into a rendezvous with the caller, which is the
// sequence reading ahead exists to break.
func readAhead(source io.ReadCloser, window int) io.ReadCloser {
	depth := window / fetch
	if depth < 1 {
		depth = 1
	}
	stream := &ahead{source: source, queue: make(chan pulled, depth)}
	go stream.pull()
	return stream
}

// pull moves the source into the queue until the source stops.
//
// The goroutine's owner is the stream it fills and Close is how it is stopped:
// closing the source is what ends a read already in flight, and the drain there
// is what guarantees a send can always land, so this always reaches its return.
func (a *ahead) pull() {
	defer close(a.queue)

	for {
		buffer := make([]byte, fetch)
		n, err := a.source.Read(buffer)
		if n > 0 {
			a.queue <- pulled{data: buffer[:n]}
		}
		if err != nil {
			a.queue <- pulled{err: err}
			return
		}
	}
}

// Read serves the caller out of the queue, waiting only when the queue is empty
// — which is the fetch not being far enough in front, and the one case where
// the caller still pays the source's latency.
//
// Why the source stopped is delivered in the queue behind the bytes rather than
// beside them, so a source that failed partway through hands over what it moved
// before it reports the failure, and reports it on every read after that.
func (a *ahead) Read(into []byte) (int, error) {
	if a.closed {
		return 0, fmt.Errorf("read a stream that is closed: %w", fs.ErrClosed)
	}

	for len(a.holds) == 0 {
		if a.failed != nil {
			return 0, a.failed
		}
		next, more := <-a.queue
		if !more {
			return 0, io.EOF
		}
		a.holds, a.failed = next.data, next.err
	}

	n := copy(into, a.holds)
	a.holds = a.holds[n:]
	return n, nil
}

// Close ends the transfer and the fetch in front of it, and reports what the
// source said about being closed.
//
// The source is closed first, because that is what ends a read already in
// flight; the drain after it is what lets the fetch reach its own return rather
// than stranding it on a send nobody is taking. Nothing outlives this call.
//
// What the queue held and the caller never read is dropped. That is the cost of
// reading ahead, and it is why a path a read consumes is never read ahead of.
func (a *ahead) Close() error {
	if a.closed {
		return nil
	}
	a.closed = true

	err := a.source.Close()
	for range a.queue { //nolint:revive // draining is the point; there is nothing to do with what is left.
	}
	return err
}
