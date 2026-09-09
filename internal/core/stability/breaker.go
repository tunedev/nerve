package stability

import (
	"errors"
	"sync"
	"time"
)

// ErrUnavailable is what a breaker answers while its circuit is open: the far
// end has failed transiently enough times in a row that the next call is not
// worth making, and refusing it now is faster and cheaper for both ends than
// another timeout.
//
// It is fail-fast by classification, being nothing Classify has been told is
// transient. That is the answer this wrapper needs from the one above it: a
// retry loop that met an open circuit stops instead of spending its remaining
// attempts on a dependency that is already failing.
var ErrUnavailable = errors.New("far end unavailable")

// Breaker is when a far end stops being called and how long for. Like Retry it
// is a value rather than a behaviour, so the policy an operator configured can
// be read and compared without running anything.
type Breaker struct {
	// Threshold is how many transient failures in a row open the circuit. Zero
	// or less is the breaker turned off, and so is the zero value.
	Threshold int

	// Cooldown is how long the circuit stays open before it lets one call
	// through to find out whether the far end has come back.
	//
	// It must be positive. A non-positive cooldown makes every call a probe,
	// which is the breaker turned off by arithmetic rather than by intent, and
	// is caught where the value is read.
	Cooldown time.Duration
}

// Circuit is the state behind one breaker: how many transient failures have
// arrived in a row, and the time until which calls are refused.
//
// It is shared by everything guarding one far end, so it is reached by pointer
// and guarded by a mutex. Callers are concurrent and a circuit is the one part
// of this vocabulary that remembers anything between them.
//
// A caller holds one Circuit per dependency and calls Guard. What counts as a
// failure worth counting stays Classify's answer rather than becoming a second
// one.
type Circuit struct {
	policy Breaker

	mu       sync.Mutex
	failures int
	// until is when the circuit reopens for a probe. The zero time is a closed
	// circuit.
	until time.Time
}

// NewCircuit returns a closed circuit under policy. One circuit is one far end:
// sharing one between two would take a working dependency down with a broken
// one, which is the whole reason every mount is built with its own.
func NewCircuit(policy Breaker) *Circuit { return &Circuit{policy: policy} }

// allow reports whether the next call may be made, refusing with ErrUnavailable
// while the circuit is open.
//
// Once the cooldown has passed, one call goes through to find out whether the
// far end is back. The wait restarts as that call leaves, so nothing follows it
// in: a probe is one call, not the resumption of traffic, and what it answers
// decides whether traffic resumes.
func (c *Circuit) allow() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.until.IsZero() {
		return nil
	}

	now := time.Now()
	if now.Before(c.until) {
		return ErrUnavailable
	}

	c.until = now.Add(c.policy.Cooldown)
	return nil
}

// record files what one call answered. Enough transient failures in a row open
// the circuit; anything else — an answer, or a failure another attempt cannot
// change — is the far end talking, so the count starts over and a probe closes
// the circuit behind it.
func (c *Circuit) record(err error) {
	if c.policy.Threshold < 1 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if Classify(err) != Retryable {
		c.failures, c.until = 0, time.Time{}
		return
	}

	c.failures++
	if c.failures >= c.policy.Threshold {
		c.until = time.Now().Add(c.policy.Cooldown)
	}
}

// Guard makes one call behind the circuit: refused with ErrUnavailable while it
// is open, filed when it is not.
func (c *Circuit) Guard(call func() error) error {
	if err := c.allow(); err != nil {
		return err
	}

	err := call()
	c.record(err)
	return err
}
