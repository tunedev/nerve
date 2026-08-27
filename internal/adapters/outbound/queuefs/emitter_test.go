package queuefs_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// docs is where the documents these cases write live, beside the queue that
// carries the work their writes make.
const docs = "/docs"

// The acceptance in one case: a document is written and an indexer does the
// work, with nobody having asked for it. The write made the job, the broker
// carried the key, and the worker came back to the tree for what changed.
func TestAWriteToTheTreeIsWorkNobodyAskedFor(t *testing.T) {
	work := newObservedBus(t)
	mustWrite(t, work.observed, docs+"/report.txt", "the content")

	indexed := make(map[string]string)
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), index(work.tree, indexed))
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}

	if got := indexed["/docs/report.txt"]; got != "the content" {
		t.Errorf("the indexer holds %q for the document, want %q", got, "the content")
	}
	if left := queued(t, work.journal); len(left) != 0 {
		t.Errorf("the queue still holds %v, want nothing", left)
	}
}

// A removal is a change like any other. The worker finds nothing where it was
// told to look, which is how an index stops serving what the tree stopped
// holding — an event that only carried a path could not say it any other way.
func TestARemovalIsWorkToo(t *testing.T) {
	work := newObservedBus(t)
	mustWrite(t, work.observed, docs+"/gone.txt", "for now")
	if err := work.observed.Remove(t.Context(), docs+"/gone.txt"); err != nil {
		t.Fatalf("Remove = %v", err)
	}

	indexed := map[string]string{"/docs/gone.txt": "for now"}
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), index(work.tree, indexed))
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}

	if held, ok := indexed["/docs/gone.txt"]; ok {
		t.Errorf("the indexer still holds %q for a document that is gone", held)
	}
}

// A queue is written to by publishing, and publishing is not a change anyone
// indexes. It is also what stops the emitter feeding itself: a job written to
// the tree that made it would make another.
func TestPublishingToTheQueueIsNotWorkOfItsOwn(t *testing.T) {
	work := newObservedBus(t)
	mustWrite(t, work.observed, busMount+"/jobs/hand/written", "by a producer")

	if left := queued(t, work.journal); !slices.Equal(left, []string{"hand/written"}) {
		t.Errorf("the queue holds %v, want only the message that was published", left)
	}
}

// A path written ten times before anyone indexes it is one job. The queue keys
// work by the path it is about, and recording replaces what is pending under a
// key, so a burst of writes costs the indexer one read of what is there now.
func TestManyWritesToOnePathAreOneJob(t *testing.T) {
	work := newObservedBus(t)
	for _, content := range []string{"first", "second", "third"} {
		mustWrite(t, work.observed, docs+"/busy.txt", content)
	}

	if left := queued(t, work.journal); !slices.Equal(left, []string{"docs/busy.txt"}) {
		t.Errorf("the queue holds %v, want one job for the path that changed", left)
	}
}

// observedBus is the tree from both ends: what a caller writes to, what the
// emitter publishes through, and the broker a worker listens on.
type observedBus struct {
	observed vfs.FileSystem
	tree     *vfs.MountTable
	journal  *memJournal
	broker   *memBroker
}

// newObservedBus mounts a queue beside a store of documents and puts an
// observer over the tree, so a write anywhere in it becomes work on the queue.
//
// The emitter publishes through the bare tree rather than the observed one. It
// would be safe either way — a queue path is read-destructive and reported to
// nobody — but nothing about what feeds what should rest on that.
func newObservedBus(t *testing.T) observedBus {
	t.Helper()

	journal := newMemJournal()
	broker := newMemBroker()
	tree := vfs.NewMountTable()
	if err := tree.Mount(busMount, queuefs.New("work", journal, queuefs.NewRelay(journal, broker, 0))); err != nil {
		t.Fatalf("Mount = %v", err)
	}
	if err := tree.Mount(docs, memfs.New()); err != nil {
		t.Fatalf("Mount = %v", err)
	}

	observed := vfs.WithWriteObserver(tree, queuefs.NewEmitter(tree, busMount))
	return observedBus{observed: observed, tree: tree, journal: journal, broker: broker}
}

// index is the handler an indexer is: it is told a key, roots it back into a
// path and reads what is there now, holding what it finds and dropping what is
// gone. Nothing it needs came in the message.
func index(tree vfs.FileSystem, held map[string]string) queuefs.Handler {
	return func(ctx context.Context, key string, _ []byte) error {
		path := "/" + key

		content, err := tree.Read(ctx, path)
		if errors.Is(err, fs.ErrNotExist) {
			delete(held, path)
			return nil
		}
		if err != nil {
			return err
		}
		held[path] = string(content)
		return nil
	}
}
