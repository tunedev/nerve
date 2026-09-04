package schedule_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/schedule"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const (
	root      = "/schedule"
	materials = "/projections"
)

// here is the workspace the jobs derive from, and began is the moment the log's
// clock reads, so a note is compared against when it was written rather than
// against now.
var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	began = time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)
)

// tree is the storage two processes share: the second one only recovers what
// the first one left because both are looking at the same mount. The log and
// what a run writes are separate areas, so a slice standing under the log's
// root is never read back as a note.
func tree(t *testing.T) vfs.FileSystem {
	t.Helper()

	table := vfs.NewMountTable()
	for _, at := range []string{root, materials} {
		if err := table.Mount(at, memfs.New()); err != nil {
			t.Fatalf("Mount(%q): %v", at, err)
		}
	}
	return table
}

// read is what fsys holds at at.
func read(t *testing.T, fsys vfs.FileSystem, at string) string {
	t.Helper()

	data, err := fsys.Read(t.Context(), at)
	if err != nil {
		t.Fatalf("Read(%q): %v", at, err)
	}
	return string(data)
}

// newScheduler returns the scheduler holder is, doing run over log and claiming
// its jobs from held.
func newScheduler(t *testing.T, log *schedule.Log, held schedule.Lease, holder string, run schedule.Run) *schedule.Scheduler {
	t.Helper()

	runner, err := schedule.New(log, held, run, holder, ttl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

// newLog returns a log over fsys whose clock reads at.
func newLog(t *testing.T, fsys vfs.FileSystem, at time.Time) *schedule.Log {
	t.Helper()

	log, err := schedule.NewLog(fsys, root, func() time.Time { return at })
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	return log
}

// The acceptance: a run that died halfway is picked up by the process after it.
//
// The first scheduler is killed rather than failed. A panic is what that looks
// like from inside the package: the run does not return, nothing after it runs,
// and nobody is told the job ended — which is what losing the host does and
// what returning an error does not.
func TestAnInterruptedJobIsRecoveredByTheProcessAfterIt(t *testing.T) {
	fsys := tree(t)
	job := schedule.Job{Scope: here, Name: "sales"}

	dying := newScheduler(t, newLog(t, fsys, began), newGrants(stopped), "node-one", func(context.Context, schedule.Job) error {
		panic("the host went away")
	})
	died := func() (killed bool) {
		defer func() { killed = recover() != nil }()
		return dying.Do(t.Context(), job) != nil
	}()
	if !died {
		t.Fatal("the run was meant to die halfway")
	}

	var ran []schedule.Job
	after := newScheduler(t, newLog(t, fsys, began), newGrants(stopped), "node-two", func(_ context.Context, job schedule.Job) error {
		ran = append(ran, job)
		return nil
	})
	if err := after.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if len(ran) != 1 || ran[0] != job {
		t.Errorf("recovered = %+v, want %+v", ran, []schedule.Job{job})
	}
}

// A job that finished is not owed. The note is what makes a job recoverable, so
// a job whose note was cleared is one the next process has no reason to run
// again — and re-running a projection nobody asked for is minutes of CPU.
func TestAJobThatFinishedIsNotRunAgain(t *testing.T) {
	fsys := tree(t)

	done := newScheduler(t, newLog(t, fsys, began), newGrants(stopped), "node-one", func(context.Context, schedule.Job) error { return nil })
	if err := done.Do(t.Context(), schedule.Job{Scope: here, Name: "sales"}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	var ran []schedule.Job
	after := newScheduler(t, newLog(t, fsys, began), newGrants(stopped), "node-two", func(_ context.Context, job schedule.Job) error {
		ran = append(ran, job)
		return nil
	})
	if err := after.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if len(ran) != 0 {
		t.Errorf("recovered = %+v, want none", ran)
	}
}

// The note is written before the work starts, not after it. A note written
// afterwards would be missing for exactly the window the death this exists for
// happens in, so the assertion is made from inside the run itself.
func TestAJobIsWrittenDownBeforeItRuns(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)
	job := schedule.Job{Scope: here, Name: "sales"}

	var owedDuring []schedule.Job
	runner := newScheduler(t, log, newGrants(stopped), "node-one", func(ctx context.Context, _ schedule.Job) error {
		var err error
		owedDuring, err = log.Unfinished(ctx)
		return err
	})
	if err := runner.Do(t.Context(), job); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if len(owedDuring) != 1 || owedDuring[0] != job {
		t.Errorf("owed while running = %+v, want %+v", owedDuring, []schedule.Job{job})
	}
}

// A run that failed stays owed. Nobody is left to be told the difference
// between a failure and a death, and the work is owed either way.
func TestAFailedRunStaysOwed(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)
	job := schedule.Job{Scope: here, Name: "sales"}

	broken := errors.New("duckdb is not answering")
	runner := newScheduler(t, log, newGrants(stopped), "node-one", func(context.Context, schedule.Job) error { return broken })
	if err := runner.Do(t.Context(), job); !errors.Is(err, broken) {
		t.Fatalf("Do of a failing run = %v, want %v", err, broken)
	}

	owed, err := log.Unfinished(t.Context())
	if err != nil {
		t.Fatalf("Unfinished: %v", err)
	}
	if len(owed) != 1 || owed[0] != job {
		t.Errorf("owed = %+v, want %+v", owed, []schedule.Job{job})
	}
}

// Recovery is a backlog rather than a sequence: the jobs are separate
// projections or separate workspaces, so one that cannot be finished costs
// itself and not the rest.
func TestRecoverRunsEveryJobAndReportsAllThatFailed(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)

	broken := errors.New("duckdb is not answering")
	stuck := schedule.Job{Scope: here, Name: "sales"}
	fine := schedule.Job{Scope: here, Name: "usage"}
	for _, job := range []schedule.Job{stuck, fine} {
		if err := log.Begin(t.Context(), job); err != nil {
			t.Fatalf("Begin(%q): %v", job.Name, err)
		}
	}

	var ran []string
	after := newScheduler(t, log, newGrants(stopped), "node-one", func(_ context.Context, job schedule.Job) error {
		ran = append(ran, job.Name)
		if job == stuck {
			return broken
		}
		return nil
	})
	err := after.Recover(t.Context())
	if !errors.Is(err, broken) {
		t.Fatalf("Recover = %v, want it to report %v", err, broken)
	}

	if len(ran) != 2 {
		t.Errorf("ran = %v, want both jobs", ran)
	}
	owed, err := log.Unfinished(t.Context())
	if err != nil {
		t.Fatalf("Unfinished: %v", err)
	}
	if len(owed) != 1 || owed[0] != stuck {
		t.Errorf("still owed = %+v, want only %+v", owed, stuck)
	}
}
