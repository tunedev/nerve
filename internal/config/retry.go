package config

import (
	"strconv"
	"time"

	"github.com/tunedev/nerve/internal/core/stability"
)

// RetryPolicy is the two retry settings in the vocabulary of the code that uses
// them: how many times a call may be made and how long the waits between them
// are. The string spelling stops here, as it does for the call timeout.
//
// Load has already validated both, so a Config that came from Load resolves to
// a usable policy. One built by hand and never validated resolves to a single
// attempt with no wait, which is retry turned off — which is why Load is the
// way in.
func (c Config) RetryPolicy() stability.Retry {
	attempts, _ := strconv.Atoi(c.RetryAttempts)
	backoff, _ := time.ParseDuration(c.RetryBackoff)
	return stability.Retry{Attempts: attempts, Backoff: backoff}
}
