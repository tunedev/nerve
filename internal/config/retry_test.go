package config_test

import (
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/stability"
)

// A checkout with no config of its own still retries, because the bottom layer
// names a policy like it names everything else.
func TestTheDefaultRetryPolicyIsUsable(t *testing.T) {
	policy := config.Defaults().RetryPolicy()

	if policy.Attempts < 1 {
		t.Errorf("default retry-attempts resolves to %d, want at least one attempt", policy.Attempts)
	}
	if policy.Backoff <= 0 {
		t.Errorf("default retry-backoff resolves to %s, want a wait between attempts", policy.Backoff)
	}
}

// The settings arrive as text and leave as a policy, so the code that retries a
// call never parses anything.
func TestTheRetrySettingsResolveToAPolicy(t *testing.T) {
	cfg, err := config.Load(config.Sources{Args: []string{"-retry-attempts", "5", "-retry-backoff", "250ms"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := stability.Retry{Attempts: 5, Backoff: 250 * time.Millisecond}
	if got := cfg.RetryPolicy(); got != want {
		t.Errorf("RetryPolicy() = %+v, want %+v", got, want)
	}
}

// One attempt is retry turned off, which is the only way to turn it off: there
// is no spelling that asks for no call at all.
func TestOneAttemptIsAllowedAndZeroIsNot(t *testing.T) {
	if _, err := config.Load(config.Sources{Args: []string{"-retry-attempts", "1"}}); err != nil {
		t.Errorf("Load with retry-attempts 1: %v", err)
	}
	if cfg, err := config.Load(config.Sources{Args: []string{"-retry-attempts", "0"}}); err == nil {
		t.Errorf("Load with retry-attempts 0 = %+v, want an error", cfg)
	}
}

// An unusable policy stops the boot rather than turning up as a call that is
// never made or a wait that never ends.
func TestAnUnusableRetryPolicyFailsAtBoot(t *testing.T) {
	for name, args := range map[string][]string{
		"attempts that are not a number": {"-retry-attempts", "a few"},
		"negative attempts":              {"-retry-attempts", "-1"},
		"empty attempts":                 {"-retry-attempts", ""},
		"backoff that is not a duration": {"-retry-backoff", "soon"},
		"backoff with no unit":           {"-retry-backoff", "100"},
		"zero backoff":                   {"-retry-backoff", "0s"},
		"negative backoff":               {"-retry-backoff", "-1s"},
	} {
		t.Run(name, func(t *testing.T) {
			if cfg, err := config.Load(config.Sources{Args: args}); err == nil {
				t.Errorf("Load with %v = %+v, want an error", args, cfg)
			}
		})
	}
}
