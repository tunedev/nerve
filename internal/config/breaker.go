package config

import (
	"strconv"
	"time"

	"github.com/tunedev/nerve/internal/core/stability"
)

// BreakerPolicy is the two breaker settings in the vocabulary of the code that
// uses them: how many transient failures in a row stop a mount being called,
// and how long it is left alone before one call probes it. The string spelling
// stops here, as it does for the retry policy and the call timeout.
//
// Load has already validated both, so a Config that came from Load resolves to
// a usable policy. One built by hand and never validated resolves to a zero
// threshold, which is the breaker turned off — which is why Load is the way in.
func (c Config) BreakerPolicy() stability.Breaker {
	threshold, _ := strconv.Atoi(c.BreakerThreshold)
	cooldown, _ := time.ParseDuration(c.BreakerCooldown)
	return stability.Breaker{Threshold: threshold, Cooldown: cooldown}
}
