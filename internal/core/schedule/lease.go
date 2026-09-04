package schedule

import (
	"context"
	"errors"
	"time"
)

// ErrHeld says the job is being run somewhere else right now, so this process
// did not run it.
//
// It is not a failure of the job. Recovery treats it as an answer and moves on
// to the next one: the node holding the lease is doing the work, and a backlog
// that stopped for it would be waiting on a process it cannot see.
var ErrHeld = errors.New("job is held by another consumer")

// Lease is the exclusion one consumer holds over one job while it runs it.
//
// The log says a job is owed; it cannot say whether somebody is on it. Two
// processes recovering the same log both find the same note and both run it,
// and two runs of one projection write the same slice files at the same time.
// A lease is what makes the second one stand down.
//
// It is a port because a real second implementation is nameable: Postgres in
// the shipped stack, and a coordination store such as etcd wherever there is
// one. What it is not is the tree. The verb set has no create-if-absent and no
// compare-and-swap, so a note on a mount can record a race that was already
// settled but cannot settle one.
//
// A grant expires rather than being held until it is given back, because the
// process holding it is the one that dies. A lease nobody can expire is a job
// no node ever runs again.
type Lease interface {
	// Acquire grants job to holder for ttl, reporting whether it was granted.
	//
	// A job a live grant already stands for is refused with false and no error:
	// another consumer has it, which is the answer rather than a failure. A
	// grant that has expired is taken over, whoever left it behind.
	Acquire(ctx context.Context, job Job, holder string, ttl time.Duration) (bool, error)

	// Release gives job back if holder still has it, so the consumer after this
	// one does not wait out the ttl. A grant somebody else holds is left alone,
	// and a job with no grant is not an error.
	Release(ctx context.Context, job Job, holder string) error
}
