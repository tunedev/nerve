package vfs

import (
	"context"
	"io"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/stability"
)

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
// consecutive failures starts over — stability.Classify is what separates the
// two, and the breaker inherits its judgement rather than making a second one.
//
// Capabilities keeps answering while the circuit is open. It is not I/O: the
// mount describes itself from the shape it already knows, so a caller can still
// learn what a path would mean when it cannot yet learn what it holds.
//
// The streaming verbs are guarded at the open and not beyond it, as they are
// bounded at the open by WithTimeout. A transfer that is already running is a
// far end that is already answering.
func WithBreaker(fsys FileSystem, policy stability.Breaker) FileSystem {
	return guarded{fsys: fsys, circuit: stability.NewCircuit(policy)}
}

// answer is Guard for a verb that returns a value.
func answer[T any](c *stability.Circuit, call func() (T, error)) (T, error) {
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
	circuit *stability.Circuit
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
	circuit *stability.Circuit
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
	circuit *stability.Circuit
}

func (c guardedTruncater) Truncate(ctx context.Context, path string, size int64) error {
	return c.circuit.Guard(func() error { return c.cut.Truncate(ctx, path, size) })
}

// guardedStreamer guards the two opens and leaves the transfers alone.
type guardedStreamer struct {
	stream  Streamer
	circuit *stability.Circuit
}

func (s guardedStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	return answer(s.circuit, func() (io.ReadCloser, error) { return s.stream.OpenRead(ctx, path) })
}

func (s guardedStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	return answer(s.circuit, func() (io.WriteCloser, error) { return s.stream.OpenWrite(ctx, path) })
}
