package vfs

import (
	"context"
	"io"
	"io/fs"
)

// WriteObserver is told what a write changed, once it has changed. It is the
// push half of keeping something derived from the tree — an index, a
// projection, a cache — in step with it, and the reason indexing is triggered
// by the write rather than by whoever made it remembering to ask.
//
// Nothing it is told carries content. A path is all a notification can honestly
// be: by the time anyone acts on it the tree has moved on, so what is there is
// read from the tree, the way a worker claims a message it was only told the
// key of.
type WriteObserver interface {
	// Wrote reports that path holds something other than what it held. What is
	// there now, and whether anything is, is read from the tree.
	Wrote(ctx context.Context, path string) error
}

// WithWriteObserver returns fsys with every write reported to observer, which
// is what makes a derived thing follow the tree without a caller carrying the
// duty to say so.
//
// It goes over the mount table rather than under it, because the path an
// indexer is told about has to be the path it can read back: a plugin sees its
// own vocabulary and would name an entry something no caller could ask for.
//
// Only a write that happened is reported, and reporting is not part of it. An
// observer that could not be told does not fail the write — the write is done,
// and a caller told otherwise would make it again, which on an append-only path
// means twice. What the observer says is dropped here for that reason, and a
// missed report is what the pulling half of the same problem is for: a derived
// thing that stamps its inputs finds the change it was never told about.
//
// A path a read consumes is never reported. Being told would make the indexer
// the reader — a queue drained by whatever was meant to be watching it — and it
// is the same rule that stops an observer publishing into the tree it observes
// from feeding itself.
func WithWriteObserver(fsys FileSystem, observer WriteObserver) FileSystem {
	return observed{fsys: fsys, observer: observer}
}

// observed is one plugin with one observer behind it. It stores nothing: what
// changed is the verb's own arguments, and whether it is worth reporting is
// asked of the mount at the path.
type observed struct {
	fsys     FileSystem
	observer WriteObserver
}

// report tells the observer about every path that changed, on the caller's
// context. A report is a call like any other and is bounded by whatever the
// observer makes it through.
func (o observed) report(ctx context.Context, paths ...string) {
	for _, path := range paths {
		if o.fsys.Capabilities(path).ReadDestructive {
			continue
		}
		_ = o.observer.Wrote(ctx, path)
	}
}

// after reports what the call changed if it changed anything, and hands back
// what the call said either way.
func (o observed) after(ctx context.Context, err error, paths ...string) error {
	if err == nil {
		o.report(ctx, paths...)
	}
	return err
}

func (o observed) Create(ctx context.Context, path string) error {
	return o.after(ctx, o.fsys.Create(ctx, path), path)
}

func (o observed) Mkdir(ctx context.Context, path string) error {
	return o.after(ctx, o.fsys.Mkdir(ctx, path), path)
}

func (o observed) Remove(ctx context.Context, path string) error {
	return o.after(ctx, o.fsys.Remove(ctx, path), path)
}

// RemoveAll reports the path it was given and not what was under it. A subtree
// that is gone is one change to whoever hears about it, and listing it to say
// so would mean listing it after it stopped being there.
func (o observed) RemoveAll(ctx context.Context, path string) error {
	return o.after(ctx, o.fsys.RemoveAll(ctx, path), path)
}

func (o observed) Read(ctx context.Context, path string) ([]byte, error) {
	return o.fsys.Read(ctx, path)
}

func (o observed) Write(ctx context.Context, path string, data []byte) error {
	return o.after(ctx, o.fsys.Write(ctx, path, data), path)
}

func (o observed) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	return o.fsys.ReadDir(ctx, path)
}

func (o observed) Stat(ctx context.Context, path string) (FileInfo, error) {
	return o.fsys.Stat(ctx, path)
}

// Rename reports both paths. One name stopped holding what it held and another
// started, and an indexer told only about the arrival serves the departure
// forever.
func (o observed) Rename(ctx context.Context, oldPath, newPath string) error {
	return o.after(ctx, o.fsys.Rename(ctx, oldPath, newPath), oldPath, newPath)
}

// Chmod is reported. What Stat says about a path is part of what a path holds,
// and only whoever is listening knows whether a mode is worth acting on.
func (o observed) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	return o.after(ctx, o.fsys.Chmod(ctx, path, mode), path)
}

// Capabilities is not a write and there is nothing to report. It is also what
// report asks about a path before telling anyone.
func (o observed) Capabilities(path string) Caps { return o.fsys.Capabilities(path) }

// AsSymlinker returns the plugin's link verbs with the one that writes
// reported, or nil if it has none. An observer says nothing about what a plugin
// can do: what it refused unobserved it refuses observed, so the caller's nil
// guard keeps telling the truth.
func (o observed) AsSymlinker() Symlinker {
	link := o.fsys.AsSymlinker()
	if link == nil {
		return nil
	}
	return observedSymlinker{link: link, observed: o}
}

// AsStreamer returns the plugin's streaming verbs with the write reported, or
// nil if it has none.
func (o observed) AsStreamer() Streamer {
	stream := o.fsys.AsStreamer()
	if stream == nil {
		return nil
	}
	return observedStreamer{stream: stream, observed: o}
}

// AsTruncater returns the plugin's truncate verb reported, or nil if it has
// none.
func (o observed) AsTruncater() Truncater {
	cut := o.fsys.AsTruncater()
	if cut == nil {
		return nil
	}
	return observedTruncater{cut: cut, observed: o}
}

// AsSubber returns the plugin's subtrees with their writes reported, or nil if
// it has none. A caller that narrowed onto a subtree is still writing to the
// tree the observer watches.
func (o observed) AsSubber() Subber {
	return SubOf(o.fsys, func(beneath FileSystem) FileSystem { return observed{fsys: beneath, observer: o.observer} })
}

type observedSymlinker struct {
	link Symlinker
	observed
}

// Symlink reports the new name. The old one is what the link stands for and is
// not touched by making it.
func (s observedSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	return s.after(ctx, s.link.Symlink(ctx, oldPath, newPath), newPath)
}

func (s observedSymlinker) Readlink(ctx context.Context, path string) (string, error) {
	return s.link.Readlink(ctx, path)
}

func (s observedSymlinker) Lstat(ctx context.Context, path string) (FileInfo, error) {
	return s.link.Lstat(ctx, path)
}

type observedTruncater struct {
	cut Truncater
	observed
}

func (c observedTruncater) Truncate(ctx context.Context, path string, size int64) error {
	return c.after(ctx, c.cut.Truncate(ctx, path, size), path)
}

// observedStreamer reports the write the caller finishes and leaves the read
// alone.
type observedStreamer struct {
	stream Streamer
	observed
}

func (s observedStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	return s.stream.OpenRead(ctx, path)
}

// OpenWrite reports when the caller closes the stream, because a streamed write
// is one write and is not complete until Close returns.
func (s observedStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	stream, err := s.stream.OpenWrite(ctx, path)
	if err != nil {
		return nil, err
	}
	return observedWriter{WriteCloser: stream, report: func() { s.report(ctx, path) }}, nil
}

// observedWriter reports what the stream wrote once it is closed. It carries
// the open's context in the closure rather than in a field: the transfer
// already runs on that context, and the report is the last thing the transfer
// does.
type observedWriter struct {
	io.WriteCloser
	report func()
}

func (w observedWriter) Close() error {
	if err := w.WriteCloser.Close(); err != nil {
		return err
	}
	w.report()
	return nil
}
