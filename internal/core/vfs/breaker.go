package vfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sync"
	"time"
)

// ErrUnavailable is what a breaker answers while its circuit is open: the far
// end has failed transiently enough times in a row that the next call is not
// worth making, and refusing it now is faster and cheaper for both ends than
// another timeout.
//
// It is fail-fast by classification, being nothing Classify has been told is
// transient. That is the answer this wrapper needs from the one above it: a
// retry loop that met an open circuit stops instead of spending its remaining
// attempts on a dependency that is already failing.
var ErrUnavailable = errors.New("far end unavailable")

// Breaker is when a far end stops being called and how long for. Like Retry it
// is a value rather than a behaviour, so the policy an operator configured can
// be read and compared without running anything.
type Breaker struct {
	// Threshold is how many transient failures in a row open the circuit. Zero
	// or less is the breaker turned off, and so is the zero value.
	Threshold int

	// Cooldown is how long the circuit stays open before it lets one call
	// through to find out whether the far end has come back.
	//
	// It must be positive. A non-positive cooldown makes every call a probe,
	// which is the breaker turned off by arithmetic rather than by intent, and
	// is caught where the value is read.
	Cooldown time.Duration
}

// WithBreaker returns fsys behind a circuit, which is what stops a mount whose
// far end is down from being called once per request until it comes back.
//
// One wrapper is one breaker. Every verb of the mount shares the state,
// accessors included, because a far end that has stopped answering has stopped
// answering all of them; and every mount gets its own, because a breaker shared
// between mounts would take a working one down with a broken one. That is the
// whole of "one failing provider degrades a feature, not a path": the subtree
// that cannot answer says so at once, and the rest of the tree never notices.
//
// Only a transient failure counts. An invalid path is a healthy answer from a
// healthy dependency, so it closes no circuit and opens none, and the count of
// consecutive failures starts over — Classify is what separates the two, and
// the breaker inherits its judgement rather than making a second one.
//
// Capabilities keeps answering while the circuit is open. It is not I/O: the
// mount describes itself from the shape it already knows, so a caller can still
// learn what a path would mean when it cannot yet learn what it holds.
//
// The streaming verbs are guarded at the open and not beyond it, as they are
// bounded at the open by WithTimeout. A transfer that is already running is a
// far end that is already answering.
func WithBreaker(fsys FileSystem, policy Breaker) FileSystem {
	return guarded{fsys: fsys, circuit: NewCircuit(policy)}
}

// Circuit is the state behind one breaker: how many transient failures have
// arrived in a row, and the time until which calls are refused.
//
// It is shared by every wrapper below, so it is reached by pointer and guarded
// by a mutex. Callers of a mount are concurrent and a breaker is the one part
// of this stack that remembers anything between them.
//
// It is exported for the reason Retry.Do is: a mount is not the only far end
// this process gives up on. A caller outside the tree holds one Circuit per
// dependency and calls Guard, and what counts as a failure worth counting stays
// Classify's answer rather than becoming a second one.
type Circuit struct {
	policy Breaker

	mu       sync.Mutex
	failures int
	// until is when the circuit reopens for a probe. The zero time is a closed
	// circuit.
	until time.Time
}

// NewCircuit returns a closed circuit under policy. One circuit is one far end:
// sharing one between two would take a working dependency down with a broken
// one, which is the whole reason every mount is built with its own.
func NewCircuit(policy Breaker) *Circuit { return &Circuit{policy: policy} }

// allow reports whether the next call may be made, refusing with ErrUnavailable
// while the circuit is open.
//
// Once the cooldown has passed, one call goes through to find out whether the
// far end is back. The wait restarts as that call leaves, so nothing follows it
// in: a probe is one call, not the resumption of traffic, and what it answers
// decides whether traffic resumes.
func (c *Circuit) allow() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.until.IsZero() {
		return nil
	}

	now := time.Now()
	if now.Before(c.until) {
		return ErrUnavailable
	}

	c.until = now.Add(c.policy.Cooldown)
	return nil
}

// record files what one call answered. Enough transient failures in a row open
// the circuit; anything else — an answer, or a failure another attempt cannot
// change — is the far end talking, so the count starts over and a probe closes
// the circuit behind it.
func (c *Circuit) record(err error) {
	if c.policy.Threshold < 1 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if Classify(err) != Retryable {
		c.failures, c.until = 0, time.Time{}
		return
	}

	c.failures++
	if c.failures >= c.policy.Threshold {
		c.until = time.Now().Add(c.policy.Cooldown)
	}
}

// Guard makes one call behind the circuit: refused with ErrUnavailable while it
// is open, filed when it is not.
func (c *Circuit) Guard(call func() error) error {
	if err := c.allow(); err != nil {
		return err
	}

	err := call()
	c.record(err)
	return err
}

// answer is Guard for a verb that returns a value.
func answer[T any](c *Circuit, call func() (T, error)) (T, error) {
	var value T
	err := c.Guard(func() error {
		var err error
		value, err = call()
		return err
	})
	return value, err
}

// guarded is one plugin behind one circuit.
type guarded struct {
	fsys    FileSystem
	circuit *Circuit
}

func (g guarded) Create(ctx context.Context, path string) error {
	return g.circuit.Guard(func() error { return g.fsys.Create(ctx, path) })
}

func (g guarded) Mkdir(ctx context.Context, path string) error {
	return g.circuit.Guard(func() error { return g.fsys.Mkdir(ctx, path) })
}

func (g guarded) Remove(ctx context.Context, path string) error {
	return g.circuit.Guard(func() error { return g.fsys.Remove(ctx, path) })
}

func (g guarded) RemoveAll(ctx context.Context, path string) error {
	return g.circuit.Guard(func() error { return g.fsys.RemoveAll(ctx, path) })
}

func (g guarded) Read(ctx context.Context, path string) ([]byte, error) {
	return answer(g.circuit, func() ([]byte, error) { return g.fsys.Read(ctx, path) })
}

func (g guarded) Write(ctx context.Context, path string, data []byte) error {
	return g.circuit.Guard(func() error { return g.fsys.Write(ctx, path, data) })
}

func (g guarded) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	return answer(g.circuit, func() ([]FileInfo, error) { return g.fsys.ReadDir(ctx, path) })
}

func (g guarded) Stat(ctx context.Context, path string) (FileInfo, error) {
	return answer(g.circuit, func() (FileInfo, error) { return g.fsys.Stat(ctx, path) })
}

func (g guarded) Rename(ctx context.Context, oldPath, newPath string) error {
	return g.circuit.Guard(func() error { return g.fsys.Rename(ctx, oldPath, newPath) })
}

func (g guarded) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	return g.circuit.Guard(func() error { return g.fsys.Chmod(ctx, path, mode) })
}

// Capabilities is not I/O and reaches no far end, so an open circuit does not
// refuse it. It is the answer a mount can still give while it cannot give the
// others; see WithBreaker.
func (g guarded) Capabilities(path string) Caps { return g.fsys.Capabilities(path) }

// AsSymlinker returns the plugin's link verbs behind the same circuit, or nil
// if it has none. A circuit says nothing about what a plugin can do: what it
// refused with a closed circuit it refuses with an open one, so the caller's
// nil guard keeps telling the truth.
func (g guarded) AsSymlinker() Symlinker {
	link := g.fsys.AsSymlinker()
	if link == nil {
		return nil
	}
	return guardedSymlinker{link: link, circuit: g.circuit}
}

// AsStreamer returns the plugin's streaming verbs with the opens guarded, or
// nil if it has none. What the opens return is not guarded; see WithBreaker.
func (g guarded) AsStreamer() Streamer {
	stream := g.fsys.AsStreamer()
	if stream == nil {
		return nil
	}
	return guardedStreamer{stream: stream, circuit: g.circuit}
}

// AsTruncater returns the plugin's truncate verb behind the same circuit, or
// nil if it has none.
func (g guarded) AsTruncater() Truncater {
	cut := g.fsys.AsTruncater()
	if cut == nil {
		return nil
	}
	return guardedTruncater{cut: cut, circuit: g.circuit}
}

// AsSubber returns the plugin's subtrees behind the same circuit, or nil if it
// has none. A subtree is the same dependency, so it is guarded by the same
// breaker rather than by one of its own.
func (g guarded) AsSubber() Subber {
	return SubOf(g.fsys, func(beneath FileSystem) FileSystem { return guarded{fsys: beneath, circuit: g.circuit} })
}

type guardedSymlinker struct {
	link    Symlinker
	circuit *Circuit
}

func (s guardedSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	return s.circuit.Guard(func() error { return s.link.Symlink(ctx, oldPath, newPath) })
}

func (s guardedSymlinker) Readlink(ctx context.Context, path string) (string, error) {
	return answer(s.circuit, func() (string, error) { return s.link.Readlink(ctx, path) })
}

func (s guardedSymlinker) Lstat(ctx context.Context, path string) (FileInfo, error) {
	return answer(s.circuit, func() (FileInfo, error) { return s.link.Lstat(ctx, path) })
}

type guardedTruncater struct {
	cut     Truncater
	circuit *Circuit
}

func (c guardedTruncater) Truncate(ctx context.Context, path string, size int64) error {
	return c.circuit.Guard(func() error { return c.cut.Truncate(ctx, path, size) })
}

// guardedStreamer guards the two opens and leaves the transfers alone.
type guardedStreamer struct {
	stream  Streamer
	circuit *Circuit
}

func (s guardedStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	return answer(s.circuit, func() (io.ReadCloser, error) { return s.stream.OpenRead(ctx, path) })
}

func (s guardedStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	return answer(s.circuit, func() (io.WriteCloser, error) { return s.stream.OpenWrite(ctx, path) })
}
