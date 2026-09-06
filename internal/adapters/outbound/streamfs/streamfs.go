// Package streamfs holds append-only streams, each a ring buffer of the most
// recent bytes written to it. It is what a log, a transcript or a trace lives
// in: writers add to the end, the oldest bytes fall out when the buffer is
// full, and a reader follows the stream rather than reading it again.
//
// It claims AppendOnly, Broadcast and ObjectStore at every path. Append-only
// because a write adds and never replaces; broadcast because every reader
// receives every append rather than taking it from another reader; object store
// because a stream name is a name and not a path through directories.
//
// The ring is what makes a stream mount bounded. A process writing to one
// forever occupies the capacity of its streams and no more, which is what lets
// a mount be left running without anybody watching how large it has grown.
package streamfs

import (
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// fileMode is what a stream is described with. A prefix a listing synthesized
// is described with keyspace.DirMode.
const fileMode = 0o644

// FS is one set of streams, safe for concurrent use.
//
// The lock here covers which streams exist, not what is in them: a stream owns
// its own bytes, so a writer appending to one never waits on a reader following
// another.
type FS struct {
	vfs.NoAccessors

	capacity int
	follow   time.Duration

	mu      sync.RWMutex
	streams map[string]*ring
}

// New returns a mount whose streams each hold capacity bytes, and whose readers
// wait follow long at the end of a stream for the next append before they end.
//
// A follow of zero is a reader that ends as soon as it has caught up, which is
// what a caller that wants what is there rather than what arrives next asks
// for.
func New(capacity int, follow time.Duration) *FS {
	return &FS{capacity: capacity, follow: follow, streams: make(map[string]*ring)}
}

// Capabilities reports an append-only broadcast keyspace, which is what
// streamfs is at every path.
func (f *FS) Capabilities(string) vfs.Caps {
	return vfs.Caps{AppendOnly: true, Broadcast: true, ObjectStore: true}
}

// find hands back the stream at name, or says it is not there.
func (f *FS) find(name string) (*ring, error) {
	at, err := keyspace.Key(name)
	if err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	stream, held := f.streams[at]
	if !held {
		return nil, fmt.Errorf("%q: %w", name, fs.ErrNotExist)
	}
	return stream, nil
}

// open hands back the stream at name, making an empty one if it is not there.
func (f *FS) open(name string) (*ring, error) {
	at, err := keyspace.Key(name)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	stream, held := f.streams[at]
	if !held {
		stream = newRing(f.capacity)
		f.streams[at] = stream
	}
	return stream, nil
}
