package pglease_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/pglease"
	"github.com/tunedev/nerve/internal/core/schedule"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// claimsEnv turns this file on. Nothing here runs without a database to talk
// to, and a lease with nothing behind it would assert the exclusion it exists
// to provide against a stub that agreed with it.
const claimsEnv = "NERVE_SCHEDULE_CLAIMS"

// expiry is how long a claim stands in these tests: long enough that a claim is
// still live when a second consumer asks about it, short enough that waiting
// one out is a test rather than a coffee break.
const expiry = 300 * time.Millisecond

// here is the workspace the jobs derive from.
var here = workspace.Scope{User: "ada", Workspace: "hunt"}

// The acceptance, at the layer that actually settles it: two consumers, one
// job, one of them told yes.
//
// They are separate leases over separate pools, which is what two nodes are
// from the database's side. Nothing in the process is shared, so the answer
// comes from the row rather than from anything Go is holding.
func TestASecondConsumerIsRefusedAClaimThatStands(t *testing.T) {
	first, second := twoNodes(t)
	job := aJob(t, first)

	held, err := first.Acquire(t.Context(), job, "node-one", expiry)
	if err != nil {
		t.Fatalf("Acquire by the first consumer: %v", err)
	}
	if !held {
		t.Fatal("the first consumer was refused a job nobody holds")
	}

	held, err = second.Acquire(t.Context(), job, "node-two", expiry)
	if err != nil {
		t.Fatalf("Acquire by the second consumer: %v", err)
	}
	if held {
		t.Error("the second consumer was granted a job another consumer holds")
	}
}

// A claim its holder never gave back is taken over once it runs out. That is
// the crash: the process that had the job is gone, so nothing will ever release
// it, and a claim nobody can expire is a job no node runs again.
func TestAClaimAConsumerDiedHoldingIsTakenOverOnceItExpires(t *testing.T) {
	first, second := twoNodes(t)
	job := aJob(t, first)

	if _, err := first.Acquire(t.Context(), job, "node-one", expiry); err != nil {
		t.Fatalf("Acquire by the consumer that then died: %v", err)
	}
	time.Sleep(expiry + 100*time.Millisecond)

	held, err := second.Acquire(t.Context(), job, "node-two", expiry)
	if err != nil {
		t.Fatalf("Acquire once the claim expired: %v", err)
	}
	if !held {
		t.Error("the claim of a dead consumer was never taken over")
	}
}

// A consumer that finished gives the job back, so the next one does not wait
// out a ttl nobody is using.
func TestAReleasedClaimIsAvailableAtOnce(t *testing.T) {
	first, second := twoNodes(t)
	job := aJob(t, first)

	if _, err := first.Acquire(t.Context(), job, "node-one", expiry); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := first.Release(t.Context(), job, "node-one"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	held, err := second.Acquire(t.Context(), job, "node-two", expiry)
	if err != nil {
		t.Fatalf("Acquire after the release: %v", err)
	}
	if !held {
		t.Error("a job that was given back was refused to the next consumer")
	}
}

// A release names its holder, so a consumer whose claim already expired and was
// taken over cannot give away the claim of the one that took it over. Coming
// back late is exactly the case where two of them would then be running.
func TestALateReleaseDoesNotDropSomebodyElsesClaim(t *testing.T) {
	first, second := twoNodes(t)
	job := aJob(t, first)

	if _, err := first.Acquire(t.Context(), job, "node-one", expiry); err != nil {
		t.Fatalf("Acquire by the consumer that then stalled: %v", err)
	}
	time.Sleep(expiry + 100*time.Millisecond)
	if _, err := second.Acquire(t.Context(), job, "node-two", expiry); err != nil {
		t.Fatalf("Acquire by the consumer that took over: %v", err)
	}

	if err := first.Release(t.Context(), job, "node-one"); err != nil {
		t.Fatalf("late Release: %v", err)
	}

	held, err := first.Acquire(t.Context(), job, "node-one", expiry)
	if err != nil {
		t.Fatalf("Acquire after the late release: %v", err)
	}
	if held {
		t.Error("a late release dropped the claim of the consumer holding the job")
	}
}

// nodes is how many consumers race for one job below. It is more than two
// because two can be settled by luck.
const nodes = 8

// Consumers that ask together are still answered one at a time. The refusal in
// the test above is one consumer arriving after another; this is all of them
// arriving at once, which is the case a lock held anywhere but inside the
// statement would get wrong.
func TestOnlyOneOfManyConsumersAskingAtOnceGetsTheJob(t *testing.T) {
	first := openLease(t)
	job := aJob(t, first)

	var granted atomic.Int64
	var asking, asked sync.WaitGroup
	asking.Add(1)
	for i := range nodes {
		held := openLease(t)
		asked.Add(1)
		go func() {
			defer asked.Done()
			asking.Wait()
			got, err := held.Acquire(context.Background(), job, fmt.Sprintf("node-%d", i), expiry)
			if err != nil {
				t.Errorf("Acquire by node-%d: %v", i, err)
				return
			}
			if got {
				granted.Add(1)
			}
		}()
	}
	asking.Done()
	asked.Wait()

	if got := granted.Load(); got != 1 {
		t.Errorf("%d of %d consumers were granted the job, want exactly 1", got, nodes)
	}
}

// twoNodes is two leases over one database, each with its own pool: what two
// processes look like from the row's side.
func twoNodes(t *testing.T) (first, second *pglease.Lease) {
	t.Helper()
	return openLease(t), openLease(t)
}

// openLease returns a lease over the database claimsEnv names, skipping the
// test when there is none.
func openLease(t *testing.T) *pglease.Lease {
	t.Helper()

	url := os.Getenv(claimsEnv)
	if url == "" {
		t.Skipf("set %s to run against Postgres; `make up` starts one", claimsEnv)
	}
	held, err := pglease.Open(url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(held.Close)
	return held
}

// aJob is a job no other run of this suite is about, and one whose claim held
// clears when the test ends so a rerun starts from nothing.
func aJob(t *testing.T, held *pglease.Lease) schedule.Job {
	t.Helper()

	job := schedule.Job{Scope: here, Name: fmt.Sprintf("sales-%d", time.Now().UnixNano())}
	t.Cleanup(func() {
		for _, holder := range []string{"node-one", "node-two"} {
			if err := held.Release(context.Background(), job, holder); err != nil {
				t.Errorf("clearing the claim of %s: %v", holder, err)
			}
		}
	})
	return job
}
