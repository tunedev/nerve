package stability

import (
	"context"
	"math/rand/v2"
	"time"
)

// Retry is how many times a call may be made and how long the waits between
// them are. It is a value rather than a behaviour, so the policy an operator
// configured can be read, printed and compared without running anything.
type Retry struct {
	// Attempts is how many times one call may be made, counting the first. One
	// is retry turned off, and so is the zero value: a call is always made
	// once.
	Attempts int

	// Backoff is the ceiling on the wait before the second attempt, doubling
	// for each attempt after that up to a minute. Each wait is a random
	// duration up to its ceiling — full jitter — because a burst of callers
	// that failed together must not come back together and fail together
	// again.
	Backoff time.Duration
}

// maxWait is the ceiling on the ceiling. Doubling with nothing to stop it
// turns a long retry into a sleep, and eventually overflows the duration.
const maxWait = time.Minute

// Do makes the call until it answers, until an error another attempt cannot
// change, or until the attempts or the caller's time run out. What the caller
// gets back is the last attempt's error rather than a summary: the failure that
// ended it is the one worth reading, and errors.Is still works on it.
//
// A caller with its own idea of what may be repeated brings that idea and
// shares this loop. A second retry loop written beside it would be a second
// answer to when a failure is worth another attempt, and the two would drift.
func (r Retry) Do(ctx context.Context, call func(context.Context) error) error {
	for made := 1; ; made++ {
		err := call(ctx)
		if err == nil || made >= r.Attempts || Classify(err) != Retryable {
			return err
		}
		if wait(ctx, r.delay(made)) != nil {
			return err
		}
	}
}

// delay is the wait after made attempts: a random duration up to Backoff
// doubled once for every attempt already made, capped at maxWait.
func (r Retry) delay(made int) time.Duration {
	ceiling := r.Backoff
	for range made - 1 {
		if ceiling >= maxWait/2 {
			ceiling = maxWait
			break
		}
		ceiling *= 2
	}

	if ceiling <= 0 {
		return 0
	}
	return rand.N(ceiling)
}

// wait blocks for d or until the caller gives up, reporting which. A caller
// that has gone gets no further attempt, so the error it ends the wait with is
// what stops the loop.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
