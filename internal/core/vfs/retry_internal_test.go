package vfs

import (
	"testing"
	"time"
)

// Every wait is a random duration up to its own ceiling. Full jitter is the
// part of backoff that is easy to leave out and expensive to leave out: a
// burst of callers that failed together return together without it, and the
// dependency that dropped them gets the same burst again.
func TestEveryWaitIsJitteredUnderADoublingCeiling(t *testing.T) {
	policy := Retry{Attempts: 10, Backoff: time.Second}

	for made, ceiling := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second} {
		seen := make(map[time.Duration]bool)
		for range 100 {
			d := policy.delay(made)
			if d < 0 || d >= ceiling {
				t.Fatalf("delay after %d attempts = %s, want a duration under %s", made, d, ceiling)
			}
			seen[d] = true
		}

		if len(seen) == 1 {
			t.Errorf("100 waits after %d attempts were all the same, want jitter", made)
		}
	}
}

// The ceiling stops doubling. A policy with attempts enough to reach it would
// otherwise turn a retry into a sleep, and eventually overflow the duration it
// is counted in.
func TestTheCeilingStopsDoubling(t *testing.T) {
	policy := Retry{Attempts: 100, Backoff: time.Second}

	for _, made := range []int{20, 60, 99} {
		if d := policy.delay(made); d < 0 || d >= maxWait {
			t.Errorf("delay after %d attempts = %s, want a duration under %s", made, d, maxWait)
		}
	}
}

// A policy with no backoff waits not at all rather than panicking on a random
// duration up to nothing. It is the zero value's answer, and the zero value is
// retry turned off.
func TestNoBackoffIsNoWait(t *testing.T) {
	if d := (Retry{}).delay(1); d != 0 {
		t.Errorf("delay with no backoff = %s, want none", d)
	}
}
