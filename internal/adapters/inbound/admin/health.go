package admin

import "sync/atomic"

// Health is what the process says about itself, and it is the whole of what a
// probe reads: one flag held in memory, never a call to Postgres or the
// broker. A probe that dials a dependency reports that dependency's health as
// this process's own, and one blip then restarts a process that was working.
//
// The zero value is a process that is alive and taking traffic.
type Health struct {
	draining atomic.Bool
}

// Drain marks the process as no longer taking new traffic. Readiness fails
// from here on while liveness keeps passing, so a balancer stops routing to a
// process that is still finishing the requests it already accepted.
func (h *Health) Drain() { h.draining.Store(true) }
