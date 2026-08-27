package queuefs

import (
	"context"
	"fmt"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Emitter is the publishing end of the work bus reached by a write rather than
// by a producer: it is told a path changed and queues the work of catching up
// with it.
//
// It publishes the way anything publishes here, by writing a path in the tree,
// so a job it makes is recorded in the journal before the broker hears about it
// and survives the broker being gone like every other message. Nothing else
// about it is special — an indexer cannot tell a job a writer asked for from
// one a write made.
//
// The job carries no body. A path is what changed and the tree is what holds
// it, and a body written now would be what the path held at the moment of a
// write the worker has not reached yet.
type Emitter struct {
	fsys  vfs.FileSystem
	mount string
}

var _ vfs.WriteObserver = (*Emitter)(nil)

// NewEmitter returns an emitter that queues work under mount, which is the
// queue's prefix in the mount table, by writing through fsys.
//
// fsys is the tree the jobs are written to and need not be the tree being
// observed. Where it is, a job makes no job of its own: a queue path is
// read-destructive and an observer reports nothing about one.
func NewEmitter(fsys vfs.FileSystem, mount string) *Emitter {
	return &Emitter{fsys: fsys, mount: mount}
}

// Wrote queues the work one changed path is.
//
// The path is the key, which is what makes a burst of writes to one path a
// single job: recording replaces what is pending under a key, so an indexer
// that arrives late reads what is there once instead of every version it
// missed. It is also what makes a worker's subscription a subtree, since a key
// is published under a topic of its parts.
func (e *Emitter) Wrote(ctx context.Context, changed string) error {
	if err := e.fsys.Write(ctx, e.job(changed), nil); err != nil {
		return fmt.Errorf("queue the work of %q: %w", changed, err)
	}
	return nil
}

// job is where the work of a changed path lives in the tree: under the queue's
// jobs path, at the mount the emitter publishes to. The whole changed path is
// the key — joining drops the root a flat key space has no room for, and the
// worker that is told the key puts it back.
func (e *Emitter) job(changed string) string {
	return path.Join(separator, e.mount, Jobs, changed)
}
