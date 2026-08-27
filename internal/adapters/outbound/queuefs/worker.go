package queuefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Handler is the work one message is. It is given the key and the body the
// worker claimed, and nil back means the work is done.
type Handler func(ctx context.Context, key string, body []byte) error

// Worker is the consuming end of the queue: it waits to be told there is work
// and then takes it off the tree.
//
// It claims through the mount table rather than through the journal, so a
// worker takes a message with the same verb any reader uses and is bounded by
// the same timeout, retried under the same policy and stopped by the same
// breaker as every other call into that mount. The broker says a key; the tree
// hands over the body.
//
// It holds no goroutine and no ticker. The listener drives it, so a worker
// nobody is publishing to is a worker doing nothing rather than a worker
// polling — the same bargain the relay makes on the publishing side, where a
// write drives the carry.
type Worker struct {
	fsys     vfs.FileSystem
	mount    string
	listener Listener
	handle   Handler
}

// NewWorker returns a worker that does what listener tells it about, claiming
// under mount, which is the queue's prefix in the mount table. Which messages
// it is told about is the listener's, because that is a standing arrangement
// with the broker rather than something a worker asks for each time.
func NewWorker(fsys vfs.FileSystem, mount string, listener Listener, handle Handler) *Worker {
	return &Worker{fsys: fsys, mount: mount, listener: listener, handle: handle}
}

// Run takes work until ctx is done, and reports what stopped it otherwise.
func (w *Worker) Run(ctx context.Context) error {
	return w.listener.Listen(ctx, w.take)
}

// take claims the message the listener heard about and hands it to the handler.
//
// A key with nothing under it is not an error. A notification outliving the
// message it is about is the ordinary case: another worker claimed it first, a
// reader took it through the tree, or a backfill is telling everyone again
// about messages claimed long ago. The claim is what settles which worker does
// the work, because a read-destructive mount can only hand a message back once.
//
// A handler that fails ends the run. The claim happened before the handler saw
// the body, so the message is out of the queue whatever the handler does:
// carrying on would lose it quietly, and putting it back would need a claim
// that expires to stop a message nobody can handle going round forever.
func (w *Worker) take(ctx context.Context, key string) error {
	body, err := w.fsys.Read(ctx, w.job(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("take %q: %w", key, err)
	}
	if err := w.handle(ctx, key, body); err != nil {
		return fmt.Errorf("handle %q: %w", key, err)
	}
	return nil
}

// job is where a key lives in the tree: under the queue's jobs path, at the
// mount the worker was pointed at.
func (w *Worker) job(key string) string {
	return path.Join(separator, w.mount, Jobs, key)
}
