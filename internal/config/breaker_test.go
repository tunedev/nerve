package config_test

import (
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/stability"
)

// A checkout with no config of its own still breaks a failing mount, because
// the bottom layer names a policy like it names everything else.
func TestTheDefaultBreakerPolicyIsUsable(t *testing.T) {
	policy := config.Defaults().BreakerPolicy()

	if policy.Threshold < 1 {
		t.Errorf("default breaker-threshold resolves to %d, want a circuit that can open", policy.Threshold)
	}
	if policy.Cooldown <= 0 {
		t.Errorf("default breaker-cooldown resolves to %s, want a wait before the probe", policy.Cooldown)
	}
}

// The settings arrive as text and leave as a policy, so the code that refuses a
// call never parses anything.
func TestTheBreakerSettingsResolveToAPolicy(t *testing.T) {
	cfg, err := config.Load(config.Sources{Args: []string{"-breaker-threshold", "8", "-breaker-cooldown", "5s"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := stability.Breaker{Threshold: 8, Cooldown: 5 * time.Second}
	if got := cfg.BreakerPolicy(); got != want {
		t.Errorf("BreakerPolicy() = %+v, want %+v", got, want)
	}
}

// An unusable policy stops the boot rather than turning up as a mount that is
// never called or a circuit that never opens.
func TestAnUnusableBreakerPolicyFailsAtBoot(t *testing.T) {
	for name, args := range map[string][]string{
		"threshold that is not a number":  {"-breaker-threshold", "a few"},
		"zero threshold":                  {"-breaker-threshold", "0"},
		"negative threshold":              {"-breaker-threshold", "-1"},
		"empty threshold":                 {"-breaker-threshold", ""},
		"cooldown that is not a duration": {"-breaker-cooldown", "later"},
		"cooldown with no unit":           {"-breaker-cooldown", "30"},
		"zero cooldown":                   {"-breaker-cooldown", "0s"},
		"negative cooldown":               {"-breaker-cooldown", "-1s"},
	} {
		t.Run(name, func(t *testing.T) {
			if cfg, err := config.Load(config.Sources{Args: args}); err == nil {
				t.Errorf("Load with %v = %+v, want an error", args, cfg)
			}
		})
	}
}
