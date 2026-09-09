package vfs

import (
	"context"
	"io"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/stability"
)

// WithRetry returns fsys with every repeatable call attempted again when the
// failure was transient, which is what stops one dropped connection from
// becoming one failed request.
//
// Two things have to be true before an attempt is repeated, and the second is
// the one that is usually skipped:
//
// The error must be retryable. stability.Classify decides, and it fails fast
// on anything nobody has marked transient — a validation error is never
// retried, because repeating an invalid call cannot make it valid and the
// dependency pays for the attempt either way.
//
// The call must be repeatable. Remove, Rename and Symlink are made once: each
// reports a different failure the second time, having already done the work,
// so a retry turns a lost answer into a wrong one. Read and Write ask the
// mount, because repeatability varies by path rather than by verb — a
// read-destructive path loses an entry to a second read, and an append-only
// path gets the same bytes twice. The streaming opens follow the verb they
// stand in for.
//
// The policy bounds the attempts; the caller's context bounds the sequence.
// Waits end when the caller does, and no attempt is made after that.
func WithRetry(fsys FileSystem, policy stability.Retry) FileSystem {
	return retrying{fsys: fsys, policy: policy}
}

// attempt is Do for a verb that returns a value. The value of the attempt
// that answered is the one that comes back.
func attempt[T any](ctx context.Context, policy stability.Retry, call func(context.Context) (T, error)) (T, error) {
	var value T
	err := policy.Do(ctx, func(ctx context.Context) error {
		var err error
		value, err = call(ctx)
		return err
	})
	return value, err
}

// retrying is one plugin behind one policy. It stores nothing per call: what
// may be repeated is either fixed by the verb or asked of the mount at the
// path, and neither is remembered.
type retrying struct {
	fsys   FileSystem
	policy stability.Retry
}

// rereadable reports whether reading path twice reads the same thing. A
// read-destructive path answers no: what one reader receives is gone, so a
// second attempt takes a second entry and loses the first.
func (r retrying) rereadable(path string) bool {
	return !r.fsys.Capabilities(path).ReadDestructive
}

// rewritable reports whether writing path twice leaves what writing it once
// left. An append-only path answers no: a write that failed may have landed,
// and repeating it appends the same bytes again.
func (r retrying) rewritable(path string) bool {
	return !r.fsys.Capabilities(path).AppendOnly
}

func (r retrying) Create(ctx context.Context, path string) error {
	return r.policy.Do(ctx, func(ctx context.Context) error {
		return r.fsys.Create(ctx, path)
	})
}

func (r retrying) Mkdir(ctx context.Context, path string) error {
	return r.policy.Do(ctx, func(ctx context.Context) error {
		return r.fsys.Mkdir(ctx, path)
	})
}

// Remove is made once. A Remove that deleted the entry and then failed to say
// so reports a missing entry on the second attempt, which is a false failure
// rather than a repeat of the call.
func (r retrying) Remove(ctx context.Context, path string) error {
	return r.fsys.Remove(ctx, path)
}

// RemoveAll is repeated: a path that is already gone is not an error, so the
// second attempt reports what the first one did.
func (r retrying) RemoveAll(ctx context.Context, path string) error {
	return r.policy.Do(ctx, func(ctx context.Context) error {
		return r.fsys.RemoveAll(ctx, path)
	})
}

func (r retrying) Read(ctx context.Context, path string) ([]byte, error) {
	read := func(ctx context.Context) ([]byte, error) { return r.fsys.Read(ctx, path) }
	if r.rereadable(path) {
		return attempt(ctx, r.policy, read)
	}
	return read(ctx)
}

func (r retrying) Write(ctx context.Context, path string, data []byte) error {
	write := func(ctx context.Context) error { return r.fsys.Write(ctx, path, data) }
	if r.rewritable(path) {
		return r.policy.Do(ctx, write)
	}
	return write(ctx)
}

func (r retrying) ReadDir(ctx context.Context, path string) ([]FileInfo, error) {
	return attempt(ctx, r.policy, func(ctx context.Context) ([]FileInfo, error) {
		return r.fsys.ReadDir(ctx, path)
	})
}

func (r retrying) Stat(ctx context.Context, path string) (FileInfo, error) {
	return attempt(ctx, r.policy, func(ctx context.Context) (FileInfo, error) {
		return r.fsys.Stat(ctx, path)
	})
}

// Rename is made once. A Rename that moved the entry and then failed to say so
// reports a missing source on the second attempt, and if something else has
// since taken the old name, moves that instead.
func (r retrying) Rename(ctx context.Context, oldPath, newPath string) error {
	return r.fsys.Rename(ctx, oldPath, newPath)
}

func (r retrying) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	return r.policy.Do(ctx, func(ctx context.Context) error {
		return r.fsys.Chmod(ctx, path, mode)
	})
}

// Capabilities is not I/O and there is nothing to attempt again. It is also
// what the verbs above ask about a path before repeating one.
func (r retrying) Capabilities(path string) Caps { return r.fsys.Capabilities(path) }

// AsSymlinker returns the plugin's link verbs behind the same policy, or nil
// if it has none. A policy says nothing about what a plugin can do: what it
// refused unwrapped it refuses wrapped, so the caller's nil guard keeps telling
// the truth.
func (r retrying) AsSymlinker() Symlinker {
	link := r.fsys.AsSymlinker()
	if link == nil {
		return nil
	}
	return retryingSymlinker{link: link, policy: r.policy}
}

// AsStreamer returns the plugin's streaming verbs with the opens retried, or
// nil if it has none. The transfer is not retried: only the plugin knows how
// far a stream got, and re-reading from the start is the caller's decision.
func (r retrying) AsStreamer() Streamer {
	stream := r.fsys.AsStreamer()
	if stream == nil {
		return nil
	}
	return retryingStreamer{stream: stream, caps: r.fsys.Capabilities, policy: r.policy}
}

// AsTruncater returns the plugin's truncate verb behind the same policy, or
// nil if it has none.
func (r retrying) AsTruncater() Truncater {
	cut := r.fsys.AsTruncater()
	if cut == nil {
		return nil
	}
	return retryingTruncater{cut: cut, policy: r.policy}
}

// AsSubber returns the plugin's subtrees behind the same policy, or nil if it
// has none.
func (r retrying) AsSubber() Subber {
	return SubOf(r.fsys, func(beneath FileSystem) FileSystem { return retrying{fsys: beneath, policy: r.policy} })
}

type retryingSymlinker struct {
	link   Symlinker
	policy stability.Retry
}

// Symlink is made once. A Symlink that made the link and then failed to say so
// reports a link already there on the second attempt.
func (s retryingSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	return s.link.Symlink(ctx, oldPath, newPath)
}

func (s retryingSymlinker) Readlink(ctx context.Context, path string) (string, error) {
	return attempt(ctx, s.policy, func(ctx context.Context) (string, error) {
		return s.link.Readlink(ctx, path)
	})
}

func (s retryingSymlinker) Lstat(ctx context.Context, path string) (FileInfo, error) {
	return attempt(ctx, s.policy, func(ctx context.Context) (FileInfo, error) {
		return s.link.Lstat(ctx, path)
	})
}

type retryingTruncater struct {
	cut    Truncater
	policy stability.Retry
}

// Truncate is repeated: cutting a path to a length it already has is the same
// path.
func (c retryingTruncater) Truncate(ctx context.Context, path string, size int64) error {
	return c.policy.Do(ctx, func(ctx context.Context) error {
		return c.cut.Truncate(ctx, path, size)
	})
}

// retryingStreamer retries the opens that are repeatable and leaves the
// transfers alone. It carries the plugin's Capabilities rather than the plugin,
// because an open is repeatable on the same terms as the verb it stands in for.
type retryingStreamer struct {
	stream Streamer
	caps   func(string) Caps
	policy stability.Retry
}

func (s retryingStreamer) OpenRead(ctx context.Context, path string) (io.ReadCloser, error) {
	open := func(ctx context.Context) (io.ReadCloser, error) { return s.stream.OpenRead(ctx, path) }
	if s.caps(path).ReadDestructive {
		return open(ctx)
	}
	return attempt(ctx, s.policy, open)
}

func (s retryingStreamer) OpenWrite(ctx context.Context, path string) (io.WriteCloser, error) {
	open := func(ctx context.Context) (io.WriteCloser, error) { return s.stream.OpenWrite(ctx, path) }
	if s.caps(path).AppendOnly {
		return open(ctx)
	}
	return attempt(ctx, s.policy, open)
}
