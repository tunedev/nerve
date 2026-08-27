package streamfs

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// ErrOverrun is what a reader gets when the bytes it had not read yet were
// dropped to make room for newer ones. It is an error rather than a silent jump
// to what is left, because a reader that skipped part of a log and was not told
// so reports a gap as an absence.
var ErrOverrun = errors.New("the reader fell behind the ring")

// ring is one stream: the most recent bytes written to it, and where in the
// stream the oldest of them sits.
//
// Offsets are absolute — counted from the first byte ever written rather than
// from the start of the window — so a reader's position stays meaningful after
// the bytes under it have been dropped, which is what lets an overrun be
// noticed instead of guessed at.
type ring struct {
	capacity int

	mu      sync.Mutex
	window  []byte
	first   int64
	mode    fs.FileMode
	modTime time.Time
	// changed is closed by the append that makes it stale and replaced with a
	// fresh one. A reader takes it while it holds the lock and waits on it
	// after letting go, so an append between the two wakes it rather than
	// leaving it waiting for the one after.
	changed chan struct{}
}

func newRing(capacity int) *ring {
	return &ring{
		capacity: capacity,
		mode:     fileMode,
		modTime:  time.Now(),
		changed:  make(chan struct{}),
	}
}

// append adds data to the end of the stream, dropping as many of the oldest
// bytes as it takes to stay inside the capacity.
func (r *ring) append(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.window = append(r.window, data...)
	if dropped := len(r.window) - r.capacity; dropped > 0 {
		r.window = r.window[:copy(r.window, r.window[dropped:])]
		r.first += int64(dropped)
	}
	r.modTime = time.Now()

	close(r.changed)
	r.changed = make(chan struct{})
}

// read hands back a copy of the window: what a reader of the whole stream gets,
// which is the most recent bytes rather than every byte ever written.
func (r *ring) read() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.window)
}

// oldest is where the window starts, which is where a reader that wants
// everything still held begins.
func (r *ring) oldest() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.first
}

// since hands back the bytes from offset on and where reading them left off.
// When there are none it hands back the channel to wait on instead, so a reader
// that is caught up has one thing to select on rather than a poll to schedule.
func (r *ring) since(offset int64) ([]byte, int64, <-chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if offset < r.first {
		return nil, 0, nil, fmt.Errorf("%d bytes were dropped before it read them: %w", r.first-offset, ErrOverrun)
	}
	end := r.first + int64(len(r.window))
	if offset >= end {
		return nil, offset, r.changed, nil
	}
	return slices.Clone(r.window[offset-r.first:]), end, nil, nil
}

// info is what the port says about the stream at name.
func (r *ring) info(name string) vfs.FileInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	return vfs.FileInfo{
		Name:    path.Base(relative(name)),
		Size:    int64(len(r.window)),
		Mode:    r.mode,
		ModTime: r.modTime,
	}
}

// chmod sets the permission bits the stream is described with. It is not a
// write, so it leaves the time the content changed where it was.
func (r *ring) chmod(mode fs.FileMode) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.mode = mode.Perm()
}
