// Package streamrotatefs holds rotating logs. A write appends to the end of a
// name; once what is there reaches the mount's segment size it is sealed under
// a numbered sibling and the name starts empty again.
//
// It is what a log that has to be kept but must not grow lives in. streamfs
// bounds a stream by dropping its oldest bytes inside a ring, where nothing
// says which bytes went. Here the drop is a whole segment and it is a name
// leaving a listing, so what a mount discarded is what a caller can see it
// discarded.
//
// It claims AppendOnly and ObjectStore at every path. Append-only because a
// write adds and never replaces; object store because a log name is a name and
// not a path through directories. Every name in the mount is a rotating log,
// sealed segments included: there is no second kind of entry here and no name
// shape that means one.
//
// Nothing optional is answered. A segment is read whole, so there is nothing to
// stream over and nothing to cut in place.
package streamrotatefs

import (
	"fmt"
	"io/fs"
	"path"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// fileMode is what a log and a segment are described with. A prefix a listing
// synthesized is described with keyspace.DirMode.
const fileMode = 0o644

// FS is one set of rotating logs, safe for concurrent use.
//
// One lock covers the whole mount rather than one per log. Every write may
// rotate, and rotation adds and removes names, so a per-log lock would have to
// take this one underneath it to finish the write it started.
type FS struct {
	vfs.NoAccessors

	size int
	keep int

	mu   sync.RWMutex
	logs map[string]*stream
}

// stream is what one name holds: the bytes written to it since it last rotated,
// and which segments it has sealed.
type stream struct {
	content []byte
	mode    fs.FileMode
	modTime time.Time

	// seq is how many times this name has rotated, and so the number the next
	// segment of it is sealed under. It only goes up, which is what keeps a
	// name a segment had from being given to another.
	seq int
	// sealed names this log's segments, oldest first. It is what retention
	// drops from, and it is why a log knows which entries of the mount are its
	// own history rather than names that merely look like it.
	sealed []string
}

// New returns a mount that seals a log once it reaches size bytes and keeps
// keep sealed segments of it.
//
// A keep of zero is a log that rotates and discards: it holds one segment and
// no history, which bounds it to size bytes and nothing more.
func New(size, keep int) *FS {
	return &FS{size: size, keep: keep, logs: make(map[string]*stream)}
}

// Capabilities reports an append-only keyspace, which is what streamrotatefs is
// at every path.
func (f *FS) Capabilities(string) vfs.Caps {
	return vfs.Caps{AppendOnly: true, ObjectStore: true}
}

// find hands back what is at name, or says it is not there. The caller holds
// the lock.
func (f *FS) find(name string) (*stream, error) {
	at, err := keyspace.Key(name)
	if err != nil {
		return nil, err
	}

	held, there := f.logs[at]
	if !there {
		return nil, fmt.Errorf("%q: %w", name, fs.ErrNotExist)
	}
	return held, nil
}

// open hands back the log at name, making an empty one if it is not there. The
// caller holds the lock.
func (f *FS) open(at string) *stream {
	held, there := f.logs[at]
	if !there {
		held = &stream{mode: fileMode, modTime: time.Now()}
		f.logs[at] = held
	}
	return held
}

// info is what the port says about the entry at name.
func (s *stream) info(name string) vfs.FileInfo {
	return vfs.FileInfo{
		Name:    path.Base(keyspace.Relative(name)),
		Size:    int64(len(s.content)),
		Mode:    s.mode,
		ModTime: s.modTime,
	}
}
