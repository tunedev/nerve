package vfs

import (
	"context"
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
// The streaming verbs are forwarded untouched. A stream outlives the open that
// returned it, so its context is not the open's: bounding the open would cut a
// healthy transfer at d. What a stream needs is an idle timeout, which belongs
// to the plugin that knows what idle means for it.
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

// AsStreamer returns the plugin's streaming verbs as they are. See WithTimeout
// for why a stream is not bounded by a call timeout.
func (b bounded) AsStreamer() Streamer { return b.fsys.AsStreamer() }

// AsTruncater returns the plugin's truncate verb behind the same bound, or nil
// if it has none.
func (b bounded) AsTruncater() Truncater {
	cut := b.fsys.AsTruncater()
	if cut == nil {
		return nil
	}
	return boundedTruncater{cut: cut, within: b.within}
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

type boundedTruncater struct {
	cut    Truncater
	within bound
}

func (c boundedTruncater) Truncate(ctx context.Context, path string, size int64) error {
	ctx, cancel := c.within.apply(ctx)
	defer cancel()
	return c.cut.Truncate(ctx, path, size)
}
