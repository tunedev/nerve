package vfs

import (
	"context"
	"io"
)

// Symlinker is the verb set of a storage kind where one path can stand for
// another. Most cannot: a queue has no links and neither does a vector index.
type Symlinker interface {
	// Symlink makes newPath stand for oldPath, which need not exist.
	Symlink(ctx context.Context, oldPath, newPath string) error

	// Readlink returns what path stands for, without following it.
	Readlink(ctx context.Context, path string) (string, error)
}

// Streamer is the verb set of a storage kind whose entries can be moved in
// pieces. It is what a caller reaches for when an entry is larger than memory,
// since Read and Write take the whole content at once.
type Streamer interface {
	// OpenRead returns the content of path as a stream. The caller closes it.
	OpenRead(ctx context.Context, path string) (io.ReadCloser, error)

	// OpenWrite replaces the content of path from a stream, creating it if it
	// does not exist. The caller closes it, and the write is not complete until
	// Close returns nil.
	OpenWrite(ctx context.Context, path string) (io.WriteCloser, error)
}

// Truncater is the verb set of a storage kind whose entries can be cut to a
// length in place, rather than only replaced whole.
type Truncater interface {
	// Truncate sets the length of path's content, padding with zero bytes if
	// size is greater than the current length.
	Truncate(ctx context.Context, path string, size int64) error
}

// NoAccessors refuses every optional verb set. A plugin embeds it and then
// overrides only the accessors it answers, so supporting nothing optional costs
// no accessor code.
//
// Each accessor returns an untyped nil, which is what makes the caller's
// `if s := fsys.AsStreamer(); s != nil` guard tell the truth. A plugin that
// overrides an accessor must return either itself or an untyped nil — never a
// nil pointer of its own type, which is non-nil once it is in an interface.
type NoAccessors struct{}

// AsSymlinker reports that the plugin has no link verbs.
func (NoAccessors) AsSymlinker() Symlinker { return nil }

// AsStreamer reports that the plugin has no streaming verbs.
func (NoAccessors) AsStreamer() Streamer { return nil }

// AsTruncater reports that the plugin has no truncate verb.
func (NoAccessors) AsTruncater() Truncater { return nil }
