package vfs

import (
	"context"
	"io/fs"
	"time"
)

// FileSystem is the verb set every storage kind answers, whatever it actually
// stores. A vector index, a job queue and a disk directory all satisfy it; what
// separates them is what they say about themselves per path, not which methods
// they have. Capabilities is where they say it.
//
// Paths are slash-separated and rooted at "/", in the vocabulary of the mount
// table rather than of any host. Implementations resolve them relative to their
// own mount point.
//
// Every verb takes a context because a mount may be remote: deadlines propagate
// through to whatever the plugin talks to. Capabilities is not a verb and takes
// none.
//
// Mode is not a parameter of creation. Chmod is the one verb that sets it,
// because most storage kinds have no mode at all and would otherwise have to
// invent one to be created.
//
// Optional verb sets are reached through the As* accessors and never by type
// assertion, because every caller holds the mount dispatcher rather than a
// plugin: an assertion would interrogate the wrapper. An accessor declared here
// forces the dispatcher to forward.
//
// Errors are the io/fs sentinels — fs.ErrNotExist, fs.ErrExist,
// fs.ErrPermission, fs.ErrInvalid — wrapped with whatever the plugin knows, and
// matched by callers with errors.Is.
type FileSystem interface {
	// Create makes path exist as an empty entry, truncating it if it already does.
	Create(ctx context.Context, path string) error

	// Mkdir makes path exist as a directory, creating missing parents.
	Mkdir(ctx context.Context, path string) error

	// Remove deletes path. It fails on a directory that is not empty.
	Remove(ctx context.Context, path string) error

	// RemoveAll deletes path and everything beneath it. A path that does not
	// exist is not an error.
	RemoveAll(ctx context.Context, path string) error

	// Read returns the whole content of path. Reading a stream in pieces is the
	// Streamer accessor's business, not this verb's.
	Read(ctx context.Context, path string) ([]byte, error)

	// Write replaces the content of path, creating it if it does not exist.
	Write(ctx context.Context, path string, data []byte) error

	// ReadDir lists the direct children of path, sorted by name.
	ReadDir(ctx context.Context, path string) ([]FileInfo, error)

	// Stat describes path without reading it.
	Stat(ctx context.Context, path string) (FileInfo, error)

	// Rename moves oldPath to newPath, replacing newPath if it exists.
	Rename(ctx context.Context, oldPath, newPath string) error

	// Chmod sets the permission bits of path.
	Chmod(ctx context.Context, path string, mode fs.FileMode) error

	// Capabilities describes what the verbs above mean at path. It is not I/O:
	// it takes no context and cannot fail, because a mount answers it from the
	// shape it already knows.
	Capabilities(path string) Caps

	// AsSymlinker returns the plugin's link verbs, or nil if it has none.
	AsSymlinker() Symlinker

	// AsStreamer returns the plugin's streaming verbs, or nil if it has none.
	AsStreamer() Streamer

	// AsTruncater returns the plugin's truncate verb, or nil if it has none.
	AsTruncater() Truncater

	// AsSubber returns the plugin's ability to hand out a subtree as a root of
	// its own, or nil if it has none.
	AsSubber() Subber
}

// FileInfo describes one entry. It is a struct rather than an interface so that
// no caller can reach past it into a concrete type: what a plugin has to say
// about an entry, it says here.
type FileInfo struct {
	// Name is the final element of the path, without any separator.
	Name string
	// Size is the length of the content in bytes, zero for a directory.
	Size int64
	// Mode carries the permission bits and the directory bit.
	Mode fs.FileMode
	// ModTime is when the content last changed.
	ModTime time.Time
}

// IsDir reports whether the entry is a directory.
func (i FileInfo) IsDir() bool { return i.Mode.IsDir() }
