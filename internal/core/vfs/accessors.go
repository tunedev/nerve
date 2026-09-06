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

	// Lstat describes path without following a link at it: a link is reported
	// as a link, with fs.ModeSymlink set and the length of its target as its
	// size, rather than as the entry it stands for.
	//
	// It lives here because only a kind with links can answer it differently
	// from Stat. Callers reach it through Lstat rather than the accessor, so
	// that a kind with no links answers with Stat instead of nothing.
	Lstat(ctx context.Context, path string) (FileInfo, error)
}

// Lstat describes path without following a link at it, whatever fsys is: the
// link verbs' Lstat where the kind has links, and Stat where it has none,
// because a kind with no links cannot have one at path.
//
// This is the call a caller writes. Reaching for the accessor directly leaves
// every kind that refuses it undescribed, when what it refused was links and
// not description.
func Lstat(ctx context.Context, fsys FileSystem, path string) (FileInfo, error) {
	if link := fsys.AsSymlinker(); link != nil {
		return link.Lstat(ctx, path)
	}
	return fsys.Stat(ctx, path)
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

// Subber is the verb set of a storage kind that can hand out one of its own
// subtrees as a filesystem whose root it is. It is what makes a subtree a
// boundary rather than a prefix: a kind that resolves paths through something
// other than the name it was given — a host disk, where the kernel follows a
// symlink long after any name was derived — contains a caller only at the root
// it was opened on.
//
// The subtree's own path is a name like any other, so a kind that resolves a
// link resolves one standing on that path too, and the subtree handed out would
// be whatever the link names — one caller's root served under another's name,
// with every verb answering. A kind answering this accessor opens the path
// without following a link at any component of it.
//
// Sub is not I/O and takes no context, for the reason Capabilities takes none:
// it narrows the address space and reads nothing. Whether the subtree is there
// is answered by the verbs called on the result.
type Subber interface {
	// Sub returns the subtree at path as a filesystem rooted there, or nil when
	// there is none to hand out — a dispatcher whose mount at path has no sub
	// verb, or a kind with nothing narrower than itself to give.
	Sub(path string) FileSystem
}

// SubOf is the Subber a wrapper answers with: the tree beneath's subtree,
// wrapped the same way, so a caller that narrowed onto one keeps whatever the
// wrapper adds. A tree beneath that hands out no subtree leaves the wrapper
// handing out none either, which is what keeps the caller's nil guard true.
func SubOf(fsys FileSystem, wrap func(FileSystem) FileSystem) Subber {
	sub := fsys.AsSubber()
	if sub == nil {
		return nil
	}
	return subOf(func(path string) FileSystem {
		beneath := sub.Sub(path)
		if beneath == nil {
			return nil
		}
		return wrap(beneath)
	})
}

// subOf is a function standing as a Subber, which is all a wrapper needs: the
// narrowing is the tree beneath's and the wrapping is a closure over it.
type subOf func(path string) FileSystem

func (f subOf) Sub(path string) FileSystem { return f(path) }

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

// AsSubber reports that the plugin hands out no subtree as a root of its own.
func (NoAccessors) AsSubber() Subber { return nil }
