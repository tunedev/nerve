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
// The scheduler holds no goroutine and no ticker. Whoever decides a projection
// is due drives it — the work bus in the shipped stack — so a scheduler with
// nothing arriving is a scheduler doing nothing rather than one polling. What
// it adds over calling the work directly is the note, and the note is the whole
// point.
package schedule

import (
	"context"
	"errors"
	"fmt"

	"github.com/tunedev/nerve/internal/core/workspace"
)

// Job is one unit of scheduled work: a named piece of derivation over one
// workspace.
//
// The name is the work rather than a run of it. Two runs of the same job are
// the same job, which is what lets a note replace the note already standing and
// what stops a log growing a row per attempt.
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
type Run func(ctx context.Context, job Job) error

// Scheduler runs jobs, writing each one down before it starts.
type Scheduler struct {
	log *Log
	run Run
}

// New returns a scheduler that does run, keeping its notes in log.
func New(log *Log, run Run) *Scheduler {
	return &Scheduler{log: log, run: run}
}

// Do runs job now, and returns what the run returned.
//
// The note is written first, because a note written afterwards is a note the
// death this exists for would have prevented. It is cleared only when the run
// says it is done: a failure and a death are the same thing to the next process
// — nobody is left to be told either way — so a job that failed stays owed and
// is recovered like one that was interrupted.
func (s *Scheduler) Do(ctx context.Context, job Job) error {
	if err := s.log.Begin(ctx, job); err != nil {
		return err
	}
	if err := s.run(ctx, job); err != nil {
		return fmt.Errorf("run job %q: %w", job.Name, err)
	}
	return s.log.End(ctx, job)
}

// Recover runs every job the log still holds, oldest first, and reports all of
// them that failed rather than stopping at the first.
//
// The jobs are independent — they are separate workspaces or separate
// projections of one — so one that cannot be finished costs itself and not the
// rest of the backlog. Each one that does finish clears its own note, and each
// one that does not stays owed for the next process.
func (s *Scheduler) Recover(ctx context.Context) error {
	owed, err := s.log.Unfinished(ctx)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}
	failures := make([]error, 0, len(owed))
	for _, job := range owed {
		if err := s.run(ctx, job); err != nil {
			failures = append(failures, fmt.Errorf("recover job %q: %w", job.Name, err))
			continue
		}
		if err := s.log.End(ctx, job); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
