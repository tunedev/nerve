package queuefs_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
)

// answerWait is how long a case gives a caller to come back once the job it was
// waiting on has finished. Everything that settles it has already run by then,
// so this is the difference between a failure and a hung run rather than a
// budget anything is expected to use.
const answerWait = 5 * time.Second

// The acceptance. A caller registers what it is waiting for, writes the job and
// blocks; it is still blocked while the work is queued, and it comes back the
// moment a worker has done it. Nothing asks a second time.
func TestACallerBlocksUntilTheJobIsDone(t *testing.T) {
	work := newBus(t)
	waiter := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))

	blocked := awaiting(t, waiter.Expect("index/42"))
	mustWrite(t, work.tree, busMount+"/jobs/index/42", "work")
	if !stillBlocked(blocked) {
		t.Fatal("the caller came back while the job was still queued, want it waiting")
	}

	var did record
	run(t, queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything),
		queuefs.Reporting(did.handle, work.broker)))
	run(t, waiter)

	if err := answer(t, blocked); err != nil {
		t.Errorf("Wait on a job that was done = %v, want nil", err)
	}
	if got := did.handled(); !slices.Equal(got, []string{"index/42=work"}) {
		t.Errorf("the worker did %v, want the job the caller waited on", got)
	}
}

// The completion names the job and says what became of it, and that is all it
// carries. A waiter reads the key and never comes back to the tree, because
// there is nothing under it to come back for.
func TestACompletionNamesTheJobAndItsOutcome(t *testing.T) {
	work := newBus(t)
	mustWrite(t, work.tree, busMount+"/jobs/index/42", "work")

	var did record
	run(t, queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything),
		queuefs.Reporting(did.handle, work.broker)))

	want := []string{"index/42", "done/ok/index/42"}
	if told := work.broker.keys(); !slices.Equal(told, want) {
		t.Errorf("the broker was told %v, want %v", told, want)
	}
}

// A job the worker could not do stops the caller waiting and says so. The
// alternative is a caller that waits out its deadline, which cannot tell a job
// that failed from a worker that is slow.
func TestACallerHearsThatTheJobWasRefused(t *testing.T) {
	work := newBus(t)
	waiter := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))

	blocked := awaiting(t, waiter.Expect("index/42"))
	mustWrite(t, work.tree, busMount+"/jobs/index/42", "work")

	refuse := func(context.Context, string, []byte) error { return errRefused }
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything),
		queuefs.Reporting(refuse, work.broker))
	if err := worker.Run(t.Context()); !errors.Is(err, errRefused) {
		t.Fatalf("Run with a handler that fails = %v, want %v", err, errRefused)
	}
	run(t, waiter)

	if err := answer(t, blocked); !errors.Is(err, queuefs.ErrRefused) {
		t.Errorf("Wait on a job the worker refused = %v, want %v", err, queuefs.ErrRefused)
	}
}

// Two callers in two processes, each waiting on its own job. Both hear both
// completions and each keeps the one it was expecting. This is why a waiter
// hears and does not claim: a claim would let the first waiter take the answer
// the second was waiting for, and the second would wait out its deadline for a
// job that was done.
func TestEveryWaiterHearsEveryCompletion(t *testing.T) {
	work := newBus(t)
	first := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))
	second := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))

	mine := awaiting(t, first.Expect("index/42"))
	yours := awaiting(t, second.Expect("mail/42"))
	mustWrite(t, work.tree, busMount+"/jobs/index/42", "indexed")
	mustWrite(t, work.tree, busMount+"/jobs/mail/42", "mailed")

	var did record
	run(t, queuefs.NewWorker(work.tree, busMount, work.broker.listen(everything),
		queuefs.Reporting(did.handle, work.broker)))
	run(t, first)
	run(t, second)

	if err := answer(t, mine); err != nil {
		t.Errorf("Wait on the indexing job = %v, want nil", err)
	}
	if err := answer(t, yours); err != nil {
		t.Errorf("Wait on the mail job = %v, want nil", err)
	}
}

// A caller whose context ends stops waiting and says why. The waiter holds
// nothing for it afterwards, so a job nobody is waiting on any more is a
// completion heard and dropped rather than a future kept forever.
func TestACallerThatGivesUpStopsWaiting(t *testing.T) {
	work := newBus(t)
	waiter := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))
	future := waiter.Expect("index/42")

	waiting, giveUp := context.WithCancel(t.Context())
	giveUp()
	if err := future.Wait(waiting); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait by a caller that gave up = %v, want %v", err, context.Canceled)
	}

	mustWrite(t, work.tree, busMount+"/jobs/index/42", "work")
	var did record
	run(t, queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything),
		queuefs.Reporting(did.handle, work.broker)))

	if err := waiter.Run(t.Context()); err != nil {
		t.Errorf("Run hearing a completion nobody waits on = %v, want nil", err)
	}
}

// A completion for a job this waiter never expected is the ordinary case, not
// an error: every waiter hears every completion and most of them are somebody
// else's.
func TestACompletionNobodyExpectedIsHeardAndDropped(t *testing.T) {
	work := newBus(t)
	mustWrite(t, work.tree, busMount+"/jobs/index/42", "work")

	var did record
	run(t, queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything),
		queuefs.Reporting(did.handle, work.broker)))

	waiter := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))
	if err := waiter.Run(t.Context()); err != nil {
		t.Errorf("Run of a waiter expecting nothing = %v, want nil", err)
	}
}

// The future is a value and not a signal: it is settled once and reports the
// same thing to everyone who reads it, including the caller that reads it
// twice.
func TestAFinishedJobReportsTheSameThingTwice(t *testing.T) {
	work := newBus(t)
	waiter := queuefs.NewWaiter(work.broker.listen(queuefs.Completions))
	future := waiter.Expect("index/42")

	mustWrite(t, work.tree, busMount+"/jobs/index/42", "work")
	refuse := func(context.Context, string, []byte) error { return errRefused }
	worker := queuefs.NewWorker(work.tree, busMount, work.broker.listen("index/"+everything),
		queuefs.Reporting(refuse, work.broker))
	if err := worker.Run(t.Context()); !errors.Is(err, errRefused) {
		t.Fatalf("Run with a handler that fails = %v, want %v", err, errRefused)
	}
	run(t, waiter)

	for _, when := range []string{"first", "second"} {
		if err := future.Wait(t.Context()); !errors.Is(err, queuefs.ErrRefused) {
			t.Errorf("the %s Wait = %v, want %v", when, err, queuefs.ErrRefused)
		}
	}
}

// runnable is a worker or a waiter: both are driven by a listener and both stop
// when it runs out or the context ends.
type runnable interface {
	Run(ctx context.Context) error
}

func run(t *testing.T, r runnable) {
	t.Helper()

	if err := r.Run(t.Context()); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

// awaiting is Wait held by somebody else, which is what a blocked caller looks
// like from a case: nothing on the channel until the job has finished.
func awaiting(t *testing.T, future *queuefs.Future) <-chan error {
	t.Helper()

	blocked := make(chan error, 1)
	go func() { blocked <- future.Wait(t.Context()) }()
	return blocked
}

// stillBlocked says whether the caller is waiting yet.
func stillBlocked(blocked <-chan error) bool {
	select {
	case <-blocked:
		return false
	default:
		return true
	}
}

// answer is what the caller came back with, or a failure if it did not.
func answer(t *testing.T, blocked <-chan error) error {
	t.Helper()

	select {
	case err := <-blocked:
		return err
	case <-time.After(answerWait):
		t.Fatal("the caller is still waiting on a job that finished")
		return nil
	}
}
