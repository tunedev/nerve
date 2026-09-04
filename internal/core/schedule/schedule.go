// Package schedule runs work that has to outlive the process running it.
//
// A projection over a workspace is minutes of somebody else's CPU, and the
// caller that asked for it went away long before it started. The interesting
// question is therefore not how a job is started but what is left when the
// process running it dies halfway: the queue that delivered the job has already
// handed it over, and a broker with destructive reads and no replay cannot be
// asked for it again.
//
// So the job is written down before it runs and the note is cleared when it is
// over. Whatever the log still holds is work that was owed and never delivered,
// and a process that starts by recovering the log picks up where the one before
// it stopped.
//
// Being recoverable makes a job runnable twice at once: every process that
// starts finds the same backlog. A lease is what keeps one of them on it, and
// the run is bounded by the lease so that exclusion granted for a while is not
// held for longer.
//
// The scheduler holds no goroutine and no ticker. Whoever decides a projection
// is due drives it — the work bus in the shipped stack — so a scheduler with
// nothing arriving is a scheduler doing nothing rather than one polling. What
// it adds over calling the work directly is the note and the claim.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/tunedev/nerve/internal/core/workspace"
)

// Job is one unit of scheduled work: a named piece of derivation over one
// workspace.
//
// The name is the work rather than a run of it. Two runs of the same job are
// the same job, which is what lets a note replace the note already standing,
// what stops a log growing a row per attempt, and what makes one lease enough
// to exclude every other consumer of it.
type Job struct {
	// Scope is the workspace the job derives from and writes into.
	Scope workspace.Scope
	// Name is what the work is, one flat segment inside the scope — the
	// manifest a projection materialises rather than a description of it.
	Name string
}

// Run is the work a job is, given to the scheduler rather than known by it: a
// kind of derivation is a handler the composition root supplies, never a branch
// here.
//
// A run must be safe to run again. Recovery cannot tell a run that died from a
// run that never started, so a job it finds written down is one it re-runs from
// the beginning, and a run that leaves half its output behind leaves it there.
//
// The context a run is given expires when the lease does. A run still going
// then has lost its exclusion, so it is cancelled rather than left to write
// beside whoever takes the job over.
type Run func(ctx context.Context, job Job) error

// Scheduler runs jobs, writing each one down before it starts and claiming it
// for as long as it runs.
type Scheduler struct {
	log    *Log
	lease  Lease
	run    Run
	holder string
	ttl    time.Duration
}

// New returns a scheduler that does run, keeping its notes in log and claiming
// each job from lease as holder for ttl.
//
// holder is who this consumer is — a host and a process, decided at
// composition. It is what a release checks against, so two consumers sharing a
// spelling of it are one consumer as far as the lease can tell.
//
// ttl is how long a claim stands, and so also how long a run may take: it has
// to outlast the work while being short enough that a job a dead host was
// holding is not owed for an afternoon.
func New(log *Log, lease Lease, run Run, holder string, ttl time.Duration) (*Scheduler, error) {
	switch {
	case log == nil:
		return nil, fmt.Errorf("scheduler has no log: %w", fs.ErrInvalid)
	case lease == nil:
		return nil, fmt.Errorf("scheduler has no lease: %w", fs.ErrInvalid)
	case run == nil:
		return nil, fmt.Errorf("scheduler has nothing to run: %w", fs.ErrInvalid)
	case holder == "":
		return nil, fmt.Errorf("scheduler names no holder: %w", fs.ErrInvalid)
	case ttl <= 0:
		return nil, fmt.Errorf("scheduler lease ttl is %s, which claims nothing: %w", ttl, fs.ErrInvalid)
	}
	return &Scheduler{log: log, lease: lease, run: run, holder: holder, ttl: ttl}, nil
}

// Do runs job now, and returns what the run returned.
//
// The note is written first, because a note written afterwards is a note the
// death this exists for would have prevented. It is written even when the claim
// is then refused: the job is owed either way, and the consumer holding it has
// written the same note under the same name.
//
// A job another consumer is running comes back as ErrHeld, having not run here.
func (s *Scheduler) Do(ctx context.Context, job Job) error {
	if err := s.log.Begin(ctx, job); err != nil {
		return err
	}
	return s.claim(ctx, job)
}

// Recover runs every job the log still holds, oldest first, and reports all of
// them that failed rather than stopping at the first.
//
// The jobs are independent — they are separate workspaces or separate
// projections of one — so one that cannot be finished costs itself and not the
// rest of the backlog. Each one that does finish clears its own note, and each
// one that does not stays owed for the next process.
//
// A job another consumer is already running is passed over rather than
// reported: it is being done, by the one process that should be doing it.
func (s *Scheduler) Recover(ctx context.Context) error {
	owed, err := s.log.Unfinished(ctx)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}
	failures := make([]error, 0, len(owed))
	for _, job := range owed {
		switch err := s.claim(ctx, job); {
		case err == nil, errors.Is(err, ErrHeld):
		default:
			failures = append(failures, fmt.Errorf("recover job %q: %w", job.Name, err))
		}
	}
	return errors.Join(failures...)
}

// claim takes job for this consumer, runs it, and gives it back.
//
// The clock starts before the lease is asked rather than after it answers, so
// the deadline the run is held to falls inside the grant the store recorded
// rather than a round trip past the end of it.
//
// The release is not deferred. A run that dies rather than returns leaves the
// claim standing until it expires, which is what a lost host does and what the
// consumer taking the job over has to wait for; a release that ran while the
// stack unwound would say the host was still there to say something.
func (s *Scheduler) claim(ctx context.Context, job Job) error {
	at := time.Now()
	held, err := s.lease.Acquire(ctx, job, s.holder, s.ttl)
	if err != nil {
		return fmt.Errorf("claim job %q: %w", job.Name, err)
	}
	if !held {
		return fmt.Errorf("job %q: %w", job.Name, ErrHeld)
	}
	return errors.Join(s.finish(ctx, job, at.Add(s.ttl)), s.lease.Release(ctx, job, s.holder))
}

// finish runs job under the deadline its claim expires at and clears the note
// when the run says it is done.
//
// The note is cleared only on success: a failure and a death are the same thing
// to the next process — nobody is left to be told either way — so a job that
// failed stays owed and is recovered like one that was interrupted. The claim
// is given back regardless, because a job owed by nobody is one the next
// consumer should be able to pick up now rather than at the end of the ttl.
func (s *Scheduler) finish(ctx context.Context, job Job, until time.Time) error {
	bounded, cancel := context.WithDeadline(ctx, until)
	defer cancel()
	if err := s.run(bounded, job); err != nil {
		return fmt.Errorf("run job %q: %w", job.Name, err)
	}
	return s.log.End(ctx, job)
}
