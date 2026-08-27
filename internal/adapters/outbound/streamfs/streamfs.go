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
	"path"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirMode and fileMode are what an entry is described with. A stream is a file;
// dirMode belongs to a prefix a listing synthesized, which exists only because
// streams are under it.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

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

// relative resolves name lexically to the part below the mount's root, so a
// caller is not made to clean paths a mount table would have cleaned. The root
// is what is left of a path that names nothing below it.
func relative(name string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(name, "/")), "/")
}

// key is the stream name addresses. The root has none: it is the prefix every
// stream of the mount is under rather than a stream itself, which is what makes
// it the one path no verb reads, removes or moves.
func key(name string) (string, error) {
	rest := relative(name)
	if rest == "" {
		return "", fmt.Errorf("%q: the root is a prefix and not a stream: %w", name, fs.ErrInvalid)
	}
	return rest, nil
}

// prefix is what a listing of name matches: every stream beneath it, and never
// the stream at it.
func prefix(name string) string {
	rest := relative(name)
	if rest == "" {
		return ""
	}
	return rest + "/"
}

// find hands back the stream at name, or says it is not there.
func (f *FS) find(name string) (*ring, error) {
	at, err := key(name)
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
	at, err := key(name)
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
