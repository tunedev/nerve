package schedule_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/schedule"
)

// ttl is how long a claim stands in these tests. It is long enough that no run
// here reaches its own deadline, so a job that was refused was refused by the
// claim rather than by a clock running out mid-test.
const ttl = time.Minute

// slice is where a run writes, standing in for the Parquet a projection
// materialises: two consumers writing it at once is what the lease exists to
// make impossible.
const slice = "/projections/sales.parquet"

// The acceptance: two consumers, one job, one writer.
//
// The second consumer asks while the first is inside its run, which is the only
// window where both could write. It is refused, and the file holds what the one
// that was granted the claim put there.
func TestTwoConsumersDoNotBothRunOneJob(t *testing.T) {
	fsys := tree(t)
	held := newGrants(stopped)
	job := schedule.Job{Scope: here, Name: "sales"}

	second := newScheduler(t, newLog(t, fsys, began), held, "node-two", func(ctx context.Context, _ schedule.Job) error {
		return fsys.Write(ctx, slice, []byte("node-two"))
	})

	var refused error
	first := newScheduler(t, newLog(t, fsys, began), held, "node-one", func(ctx context.Context, job schedule.Job) error {
		refused = second.Do(ctx, job)
		return fsys.Write(ctx, slice, []byte("node-one"))
	})
	if err := first.Do(t.Context(), job); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if !errors.Is(refused, schedule.ErrHeld) {
		t.Errorf("the second consumer got %v, want %v", refused, schedule.ErrHeld)
	}
	if written := read(t, fsys, slice); written != "node-one" {
		t.Errorf("%s = %q, want the consumer that held the claim to be the only writer", slice, written)
	}
}

// A claim a dead consumer left behind is waited out, not taken.
//
// The first consumer is killed rather than failed — a panic, because that is
// what losing a host looks like from inside the package: the run does not
// return, so nothing gives the claim back. The next consumer stands down while
// the claim is live and takes the job over once it expires.
func TestAJobHeldByADeadConsumerIsRecoveredOnceTheClaimExpires(t *testing.T) {
	fsys := tree(t)
	moving := &clock{at: began}
	held := newGrants(moving.now)
	job := schedule.Job{Scope: here, Name: "sales"}

	dying := newScheduler(t, newLog(t, fsys, began), held, "node-one", func(context.Context, schedule.Job) error {
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
	after := newScheduler(t, newLog(t, fsys, began), held, "node-two", func(_ context.Context, job schedule.Job) error {
		ran = append(ran, job)
		return nil
	})
	if err := after.Recover(t.Context()); err != nil {
		t.Fatalf("Recover while the claim stands: %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("recovered %+v while another consumer's claim was live, want none", ran)
	}

	moving.advance(ttl + time.Second)
	if err := after.Recover(t.Context()); err != nil {
		t.Fatalf("Recover once the claim expired: %v", err)
	}
	if len(ran) != 1 || ran[0] != job {
		t.Errorf("recovered = %+v, want %+v", ran, []schedule.Job{job})
	}
}

// A consumer that finished gives the job back rather than sitting on it, so the
// next one that wants it does not wait out a ttl nobody is using.
func TestAFinishedJobIsGivenBackBeforeTheClaimExpires(t *testing.T) {
	fsys := tree(t)
	held := newGrants(stopped)
	job := schedule.Job{Scope: here, Name: "sales"}

	first := newScheduler(t, newLog(t, fsys, began), held, "node-one", func(context.Context, schedule.Job) error { return nil })
	if err := first.Do(t.Context(), job); err != nil {
		t.Fatalf("Do: %v", err)
	}

	var ran []schedule.Job
	second := newScheduler(t, newLog(t, fsys, began), held, "node-two", func(_ context.Context, job schedule.Job) error {
		ran = append(ran, job)
		return nil
	})
	if err := second.Do(t.Context(), job); err != nil {
		t.Fatalf("Do by the consumer after it: %v", err)
	}

	if len(ran) != 1 || ran[0] != job {
		t.Errorf("the second consumer ran %+v, want %+v", ran, []schedule.Job{job})
	}
}

// A run that failed gives the claim back too, and the note stays. The work is
// still owed, and the next consumer can pick it up now rather than at the end
// of a ttl held by a process that is no longer doing anything.
func TestAFailedRunGivesTheJobBackAndStaysOwed(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)
	held := newGrants(stopped)
	job := schedule.Job{Scope: here, Name: "sales"}

	broken := errors.New("duckdb is not answering")
	first := newScheduler(t, log, held, "node-one", func(context.Context, schedule.Job) error { return broken })
	if err := first.Do(t.Context(), job); !errors.Is(err, broken) {
		t.Fatalf("Do of a failing run = %v, want %v", err, broken)
	}

	var ran []schedule.Job
	second := newScheduler(t, log, held, "node-two", func(_ context.Context, job schedule.Job) error {
		ran = append(ran, job)
		return nil
	})
	if err := second.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(ran) != 1 || ran[0] != job {
		t.Errorf("recovered = %+v, want %+v", ran, []schedule.Job{job})
	}
}

// A run is held to the claim it is running under. Exclusion that outlives its
// lease is not exclusion: past the deadline the job belongs to whoever takes it
// over, so a run still going then is cancelled rather than left writing beside
// them.
func TestARunIsBoundedByTheClaim(t *testing.T) {
	fsys := tree(t)
	held := newGrants(stopped)

	var deadline time.Time
	var bounded bool
	runner := newScheduler(t, newLog(t, fsys, began), held, "node-one", func(ctx context.Context, _ schedule.Job) error {
		deadline, bounded = ctx.Deadline()
		return nil
	})
	if err := runner.Do(t.Context(), schedule.Job{Scope: here, Name: "sales"}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if !bounded {
		t.Fatal("the run was given no deadline, want one at the end of the claim")
	}
	if latest := time.Now().Add(ttl); deadline.After(latest) {
		t.Errorf("run deadline is %s, which is past the claim ending by %s", deadline, latest)
	}
}

// grants is the exclusion the consumers in these tests share: one live claim
// per job, kept in memory because what is asserted here is the scheduler rather
// than a store. Its clock is the test's, so a claim expires when the test says
// it does instead of when it waits.
type grants struct {
	mu   sync.Mutex
	now  func() time.Time
	held map[schedule.Job]claim
}

// claim is one live grant: who has the job, and until when.
type claim struct {
	holder string
	until  time.Time
}

// stopped is the clock of a test where no claim has to expire.
func stopped() time.Time { return began }

// newGrants returns a lease reading now for its clock.
func newGrants(now func() time.Time) *grants {
	return &grants{now: now, held: map[schedule.Job]claim{}}
}

// Acquire grants job unless a claim on it is still live.
func (g *grants) Acquire(_ context.Context, job schedule.Job, holder string, ttl time.Duration) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if standing, ok := g.held[job]; ok && standing.until.After(g.now()) {
		return false, nil
	}
	g.held[job] = claim{holder: holder, until: g.now().Add(ttl)}
	return true, nil
}

// Release drops the claim if holder is the one standing.
func (g *grants) Release(_ context.Context, job schedule.Job, holder string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if standing, ok := g.held[job]; ok && standing.holder == holder {
		delete(g.held, job)
	}
	return nil
}

// clock is a time the test moves, so an expiry is asserted rather than waited
// for.
type clock struct {
	mu sync.Mutex
	at time.Time
}

// now reads the clock.
func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// advance moves the clock forward by by.
func (c *clock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(by)
}
