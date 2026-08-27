package queuefs_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// busMount is where the queue is in the tree these cases write to. A worker is
// pointed at the same prefix, because the bus is the mount and not a second
// thing beside it.
const busMount = "/queue"

// everything is the pattern of a worker that wants the whole queue.
const everything = "#"

// errRefused is what a handler that cannot do the work says, and errCrashed is
// what a worker whose process died looks like from the listener.
var (
	errRefused = errors.New("the handler refused the work")
	errCrashed = errors.New("the worker died")
)

// The publish path and the handle path in one case: two messages are written to
// the tree, and a worker that never learns where the queue is stored does both
// and empties it.
func TestAWrittenMessageReachesAWorker(t *testing.T) {
	work := newBus(t)
	mustWrite(t, work.tree, busMount+"/jobs/index/document/42", "work")
	mustWrite(t, work.tree, busMount+"/jobs/index/document/43", "more")

	var did record
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), did.handle)
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}

	want := []string{"index/document/42=work", "index/document/43=more"}
	if got := did.handled(); !slices.Equal(got, want) {
		t.Errorf("the worker did %v, want %v", got, want)
	}
	if left := queued(t, work.journal); len(left) != 0 {
		t.Errorf("the queue still holds %v, want nothing", left)
	}
}

// Two workers hear about the same message and one of them does it. The claim is
// what settles it: the tree hands a message back once, so the second worker
// finds nothing under the key it was told about and carries on.
func TestOnlyOneWorkerTakesAMessage(t *testing.T) {
	work := newBus(t)
	mustWrite(t, work.tree, busMount+"/jobs/only", "once")

	var first, second record
	for _, worker := range []*queuefs.Worker{
		queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), first.handle),
		queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), second.handle),
	} {
		if err := worker.Run(t.Context()); err != nil {
			t.Fatalf("Run = %v", err)
		}
	}

	if did := append(first.handled(), second.handled()...); !slices.Equal(did, []string{"only=once"}) {
		t.Errorf("the message was done %v, want it done once", did)
	}
}

// A worker hears about the part of the queue it asked for and leaves the rest
// queued, which is how one broker carries work for more than one kind of
// worker without anything having to route it.
func TestAWorkerHearsOnlyItsPattern(t *testing.T) {
	work := newBus(t)
	mustWrite(t, work.tree, busMount+"/jobs/index/42", "indexed")
	mustWrite(t, work.tree, busMount+"/jobs/mail/42", "mailed")

	var did record
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything), did.handle)
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}

	if got := did.handled(); !slices.Equal(got, []string{"index/42=indexed"}) {
		t.Errorf("the indexing worker did %v, want only what it asked for", got)
	}
	if left := queued(t, work.journal); !slices.Equal(left, []string{"mail/42"}) {
		t.Errorf("the queue holds %v, want the message no worker asked for", left)
	}
}

// The crash simulation on the publishing side. A process records three messages
// and dies with the broker never having been told about one of them, so nothing
// was distributed and nothing was listening. The process that comes next shares
// only the journal with it, and every message is still done exactly once.
func TestACrashBeforeTheBrokerWasToldLosesNoWork(t *testing.T) {
	journal := newMemJournal()

	// The process that dies. Its broker is not there for the whole of its life.
	gone := newMemBroker()
	gone.refuse(errGone)
	dying := mount(t, journal, gone)
	for _, name := range []string{"first", "second", "third"} {
		mustWrite(t, dying, busMount+"/jobs/"+name, name)
	}
	if told := gone.keys(); len(told) != 0 {
		t.Fatalf("the broker was told %v before the crash, want nothing", told)
	}

	// The process that comes after it: a new tree, a new relay and a broker
	// that answers, over the journal the dead one left behind. Its first write
	// carries what the crash never did.
	broker := newMemBroker()
	tree := mount(t, journal, broker)
	mustWrite(t, tree, busMount+"/jobs/fourth", "fourth")

	var did record
	worker := queuefs.NewWorker(tree, busMount, broker.listen(everything), did.handle)
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}

	want := []string{"first=first", "second=second", "third=third", "fourth=fourth"}
	if got := did.handled(); !slices.Equal(got, want) {
		t.Errorf("the worker did %v, want %v", got, want)
	}
	if left := queued(t, journal); len(left) != 0 {
		t.Errorf("the queue still holds %v after the crash, want nothing", left)
	}
}

// The crash simulation on the handling side. A worker dies part way through its
// run: what it claimed it did, and the rest it never claimed. The worker that
// comes after it is told about all of it again — an unacknowledged notification
// comes back — and finishes the work without doing anything twice.
func TestACrashMidRunLosesNoWork(t *testing.T) {
	work := newBus(t)
	for _, name := range []string{"first", "second", "third"} {
		mustWrite(t, work.tree, busMount+"/jobs/"+name, name)
	}

	var before record
	dying := queuefs.NewWorker(work.tree, busMount, crashAfter(work.broker.listen(everything), 1), before.handle)
	if err := dying.Run(t.Context()); !errors.Is(err, errCrashed) {
		t.Fatalf("Run of the worker that dies = %v, want %v", err, errCrashed)
	}

	var after record
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), after.handle)
	if err := worker.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}

	want := []string{"first=first", "second=second", "third=third"}
	if did := append(before.handled(), after.handled()...); !slices.Equal(did, want) {
		t.Errorf("the two workers did %v, want %v", did, want)
	}
	if left := queued(t, work.journal); len(left) != 0 {
		t.Errorf("the queue still holds %v after the crash, want nothing", left)
	}
}

// A handler that cannot do the work ends the run and says why. The message it
// was given is gone — claiming it was what took it out of the queue — and the
// one behind it is still queued for whoever runs next, which is the difference
// between losing one message loudly and losing every message quietly.
func TestAHandlerThatFailsEndsTheRun(t *testing.T) {
	work := newBus(t)
	mustWrite(t, work.tree, busMount+"/jobs/first", "one")
	mustWrite(t, work.tree, busMount+"/jobs/second", "two")

	refuse := func(context.Context, string, []byte) error { return errRefused }
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything), refuse)

	if err := worker.Run(t.Context()); !errors.Is(err, errRefused) {
		t.Fatalf("Run with a handler that fails = %v, want %v", err, errRefused)
	}
	if left := queued(t, work.journal); !slices.Equal(left, []string{"second"}) {
		t.Errorf("the queue holds %v, want the message the worker never reached", left)
	}
}

// bus is one queue seen from both ends: the tree a producer writes to, and the
// broker a worker listens on.
type bus struct {
	tree    *vfs.MountTable
	journal *memJournal
	broker  *memBroker
}

func newBus(t *testing.T) bus {
	t.Helper()

	journal := newMemJournal()
	broker := newMemBroker()
	return bus{tree: mount(t, journal, broker), journal: journal, broker: broker}
}

// mount puts a queue over journal and broker into a tree of its own, which is
// what a process holding that queue looks like. Two of them over one journal is
// two processes.
func mount(t *testing.T, journal *memJournal, broker *memBroker) *vfs.MountTable {
	t.Helper()

	tree := vfs.NewMountTable()
	if err := tree.Mount(busMount, queuefs.New("work", journal, queuefs.NewRelay(journal, broker, 0))); err != nil {
		t.Fatalf("Mount = %v", err)
	}
	return tree
}

// queued is what the journal still holds, by key. It asks the journal rather
// than the tree because listing through the tree is fine and reading through it
// would consume what the case is counting.
func queued(t *testing.T, journal queuefs.Journal) []string {
	t.Helper()

	entries, err := journal.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	keys := make([]string, len(entries))
	for i, entry := range entries {
		keys[i] = entry.Key
	}
	return keys
}

// record is what a handler was given, which is the only evidence the handle
// path ran at all.
type record struct {
	mu   sync.Mutex
	done []string
}

func (r *record) handle(_ context.Context, key string, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done = append(r.done, key+"="+string(body))
	return nil
}

func (r *record) handled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.done)
}

// crashing is a listener that stops part way through, which is what a worker
// whose process died looks like with notifications still to come.
type crashing struct {
	listener queuefs.Listener
	before   int
}

func crashAfter(listener queuefs.Listener, notifications int) queuefs.Listener {
	return &crashing{listener: listener, before: notifications}
}

func (c *crashing) Listen(ctx context.Context, notify queuefs.Notify) error {
	return c.listener.Listen(ctx, func(ctx context.Context, key string) error {
		if c.before == 0 {
			return errCrashed
		}
		c.before--
		return notify(ctx, key)
	})
}
