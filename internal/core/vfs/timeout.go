package vfs

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// WithTimeout returns fsys with every verb bounded by d, which is what stops a
// mount that talks to something remote from hanging its caller forever.
//
// The bound is a ceiling and never an extension: context.WithTimeout keeps
// whichever deadline comes first, so a caller with less time left than d keeps
// its own and the plugin — with whatever driver it holds — sees that one.
//
// d must be positive. A non-positive d expires every call before it starts,
// which is a mistake at composition rather than a condition at runtime, and is
// caught where the value is read rather than defended against here.
//
// The streaming verbs are bounded twice, because an open is a discrete call and
// a transfer is not. The open gives up after d. The stream it returned runs on
// the caller's context until the caller closes it, bounded not by how long it
// takes — a healthy download is as long as the entry — but by how long it can
// move nothing: d with no byte crossing ends it. This is the split every
// network client makes between dialling and reading.
func WithTimeout(fsys FileSystem, d time.Duration) FileSystem {
	return bounded{fsys: fsys, within: bound(d)}
}

// bound is a duration that hands out deadlines, so the three types below share
// one answer to "what does bounding a call mean" instead of three.
type bound time.Duration

// apply gives ctx the bound. The caller cancels.
func (b bound) apply(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(b))
}

// open bounds an open without bounding what it returns. The transfer runs on
// the caller's context, so cancelling a request still stops it; the timer ends
// that context if the open overruns, and stopping the timer lifts the bound
// once the open has answered.
//
// inTime reports whether the open beat the bound. release ends the transfer,
// and is the caller's to call when it closes the stream.
func (b bound) open(ctx context.Context) (transfer context.Context, inTime func() bool, release context.CancelFunc) {
	transfer, release = context.WithCancel(ctx)
	timer := time.AfterFunc(time.Duration(b), release)
	return transfer, timer.Stop, release
}

// openWithin runs one open under the bound and hands back the stream with the
// context it runs on. An open that overruns is a call that never answered, so
// it reads as a deadline even though the plugin saw a cancellation, and a
// stream that arrives late is closed rather than handed to a caller that has
// already been told it failed.
func openWithin[T io.Closer](ctx context.Context, within bound, open func(context.Context) (T, error)) (T, context.CancelFunc, error) {
	transfer, inTime, release := within.open(ctx)
	stream, err := open(transfer)

	var none T
	if !inTime() {
		release()
		if err == nil {
			_ = stream.Close()
		}
		return none, nil, context.DeadlineExceeded
	}
	if err != nil {
		release()
		return none, nil, err
	}
	return stream, release, nil
}

// bounded is one plugin behind one deadline. It stores nothing and resolves
// nothing: every verb gives the call a deadline and hands it on.
type bounded struct {
	fsys   FileSystem
	within bound
}

func (b bounded) Create(ctx context.Context, path string) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Create(ctx, path)
}

func (b bounded) Mkdir(ctx context.Context, path string) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Mkdir(ctx, path)
}

func (b bounded) Remove(ctx context.Context, path string) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Remove(ctx, path)
}

func (b bounded) RemoveAll(ctx context.Context, path string) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.RemoveAll(ctx, path)
}

func (b bounded) Read(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Read(ctx, path)
}

func (b bounded) Write(ctx context.Context, path string, data []byte) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Write(ctx, path, data)
}

func (b bounded) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.ReadDir(ctx, path)
}

func (b bounded) Stat(ctx context.Context, path string) (FileInfo, error) {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Stat(ctx, path)
}

func (b bounded) Rename(ctx context.Context, oldPath, newPath string) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Rename(ctx, oldPath, newPath)
}

func (b bounded) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	ctx, cancel := b.within.apply(ctx)
	defer cancel()
	return b.fsys.Chmod(ctx, path, mode)
}

// Capabilities is not I/O and takes no context, so there is nothing to bound.
func (b bounded) Capabilities(path string) Caps { return b.fsys.Capabilities(path) }

// AsSymlinker returns the plugin's link verbs behind the same bound, or nil if
// it has none. A bound says nothing about what a plugin can do: what it refused
// unbounded it refuses bounded, so the caller's nil guard keeps telling the
// truth.
func (b bounded) AsSymlinker() Symlinker {
	link := b.fsys.AsSymlinker()
	if link == nil {
		return nil
	}
	return boundedSymlinker{link: link, within: b.within}
}

// AsStreamer returns the plugin's streaming verbs bounded, or nil if it has
// none. The opens are bounded by a deadline and the transfers by idleness; see
// WithTimeout.
func (b bounded) AsStreamer() Streamer {
	stream := b.fsys.AsStreamer()
	if stream == nil {
		return nil
	}
	return boundedStreamer{stream: stream, within: b.within}
}

// AsTruncater returns the plugin's truncate verb behind the same bound, or nil
// if it has none.
func (b bounded) AsTruncater() Truncater {
	cut := b.fsys.AsTruncater()
	if cut == nil {
		return nil
	}
	return boundedTruncater{cut: cut, within: b.within}
}

// AsSubber returns the plugin's subtrees under the same bound, or nil if it has
// none.
func (b bounded) AsSubber() Subber {
	return SubOf(b.fsys, func(_ string, beneath FileSystem) FileSystem { return bounded{fsys: beneath, within: b.within} })
}

type boundedSymlinker struct {
	link   Symlinker
	within bound
}

func (s boundedSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	ctx, cancel := s.within.apply(ctx)
	defer cancel()
	return s.link.Symlink(ctx, oldPath, newPath)
}

func (s boundedSymlinker) Readlink(ctx context.Context, path string) (string, error) {
	ctx, cancel := s.within.apply(ctx)
	defer cancel()
	return s.link.Readlink(ctx, path)
}

func (s boundedSymlinker) Lstat(ctx context.Context, path string) (FileInfo, error) {
	ctx, cancel := s.within.apply(ctx)
	defer cancel()
	return s.link.Lstat(ctx, path)
}

type boundedTruncater struct {
	cut    Truncater
	within bound
}

func (c boundedTruncater) Truncate(ctx context.Context, path string, size int64) error {
	ctx, cancel := c.within.apply(ctx)
	defer cancel()
	return c.cut.Truncate(ctx, path, size)
}

// boundedStreamer bounds the two opens with a deadline and the transfers they
// return with an idle bound.
type boundedStreamer struct {
	stream Streamer
	within bound
}

func (s boundedStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	stream, release, err := openWithin(ctx, s.within, func(ctx context.Context) (io.ReadCloser, error) {
		return s.stream.OpenRead(ctx, path)
	})
	if err != nil {
		return nil, err
	}
	return streamReader{stream: stream, idle: watch(time.Duration(s.within), release)}, nil
}

func (s boundedStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	stream, release, err := openWithin(ctx, s.within, func(ctx context.Context) (io.WriteCloser, error) {
		return s.stream.OpenWrite(ctx, path)
	})
	if err != nil {
		return nil, err
	}
	return streamWriter{stream: stream, idle: watch(time.Duration(s.within), release)}, nil
}
