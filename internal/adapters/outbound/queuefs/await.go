package queuefs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrRefused is what awaiting a job the worker could not do reports. Why it
// could not is where the worker ran: a completion is a key, and a key carries
// the outcome and not the reason.
var ErrRefused = errors.New("the job was not done")

// Waiter is the awaiting end of the work bus: it holds what callers are waiting
// for and settles each future when the completion naming its job arrives.
//
// It is not a worker. A worker claims, because only one of them may do a job; a
// waiter only hears, because every caller waiting on a job needs the answer and
// a claim would let one of them take another's. So a waiter needs a queue of
// its own on the broker — two sharing one queue name compete, and one of them
// waits out a deadline for a completion the other was handed.
//
// It holds no goroutine and no ticker. The listener drives it, which is the
// same bargain the worker makes: a caller waiting on a job that has not
// finished is a caller blocked on a channel rather than one asking again.
type Waiter struct {
	listener Listener

	mu      sync.Mutex
	waiting map[string]*Future
}

// NewWaiter returns a waiter settled by what listener hears. The listener binds
// to Completions, because which completions a waiter hears about is a standing
// arrangement with the broker rather than something it asks for per job.
func NewWaiter(listener Listener) *Waiter {
	return &Waiter{listener: listener, waiting: make(map[string]*Future)}
}

// Run hears completions until ctx is done, and reports what stopped it
// otherwise.
func (w *Waiter) Run(ctx context.Context) error {
	return w.listener.Listen(ctx, w.hear)
}

// Expect registers interest in key and returns the future its completion
// settles.
//
// It is called before the job is written. A completion that arrives for a key
// nobody expected has nobody to tell, and a caller that published first and
// registered second could be that nobody — the job may be done before its
// producer's next statement runs.
//
// Two calls under one key are one future: a key is one message, so waiting on
// it twice is two callers waiting on the same thing.
func (w *Waiter) Expect(key string) *Future {
	w.mu.Lock()
	defer w.mu.Unlock()

	if future, waiting := w.waiting[key]; waiting {
		return future
	}
	future := &Future{waiter: w, key: key, settled: make(chan struct{})}
	w.waiting[key] = future
	return future
}

// hear settles the future waiting on the job a completion names.
//
// A completion nobody expected is not an error, and is the ordinary case:
// every waiter hears every completion, so most of what any one of them hears is
// about somebody else's job. A key that is not a completion at all is ignored
// for the same reason — a waiter that ended its run over one strange key would
// take every future it holds down with it.
func (w *Waiter) hear(_ context.Context, key string) error {
	outcome, job, isCompletion := completed(key)
	if !isCompletion {
		return nil
	}

	w.mu.Lock()
	future, waiting := w.waiting[job]
	delete(w.waiting, job)
	w.mu.Unlock()

	if waiting {
		future.settle(outcome)
	}
	return nil
}

// forget drops what a caller stopped waiting for, so a waiter holds futures for
// outstanding waits and nothing else. It drops this future and not whatever is
// under the key now: a key waited on again after this one settled is a second
// job, and a first caller's deadline is no business of its.
func (w *Waiter) forget(f *Future) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.waiting[f.key] == f {
		delete(w.waiting, f.key)
	}
}

// completed reads a completion key back into the outcome and the job it is
// about, and says whether it was one.
func completed(key string) (string, string, bool) {
	under, isCompletion := strings.CutPrefix(key, Done+separator)
	if !isCompletion {
		return "", "", false
	}

	outcome, job, split := strings.Cut(under, separator)
	if !split || (outcome != ok && outcome != failed) {
		return "", "", false
	}
	return outcome, job, true
}

// Future is a job's completion before it has arrived: one value that will
// arrive, waited on by whoever holds it, settled once and readable after.
type Future struct {
	waiter  *Waiter
	key     string
	settled chan struct{}
	failure error
}

// Wait blocks until the job finished or ctx is done, and reports ErrRefused for
// a job the worker could not do.
//
// A caller that gives up is a caller nothing is holding a future for any more.
// Waiting again after the job finished reports what it reported the first time,
// which is what makes this a future rather than a signal.
func (f *Future) Wait(ctx context.Context) error {
	select {
	case <-f.settled:
		return f.failure
	case <-ctx.Done():
		f.waiter.forget(f)
		return fmt.Errorf("await %q: %w", f.key, ctx.Err())
	}
}

// settle records what became of the job and wakes everyone waiting. It is
// called once: the waiter takes the future out from under its lock, so only the
// caller that took it settles it.
func (f *Future) settle(outcome string) {
	if outcome == failed {
		f.failure = fmt.Errorf("await %q: %w", f.key, ErrRefused)
	}
	close(f.settled)
}
