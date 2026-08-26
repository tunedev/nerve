package config_test

import (
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/config"
)

// A checkout with no config of its own still bounds its calls, because the
// bottom layer names a timeout like it names everything else.
func TestTheDefaultCallTimeoutIsPositive(t *testing.T) {
	if d := config.Defaults().CallTimeoutDuration(); d <= 0 {
		t.Errorf("default call-timeout resolves to %s, want time an operator can use", d)
	}
}

// The setting arrives as text and leaves as a duration, so the code that bounds
// a call never parses anything.
func TestTheCallTimeoutResolvesToADuration(t *testing.T) {
	cfg, err := config.Load(config.Sources{Args: []string{"-call-timeout", "250ms"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.CallTimeoutDuration(); got != 250*time.Millisecond {
		t.Errorf("CallTimeoutDuration() = %s, want %s", got, 250*time.Millisecond)
	}
}

// A timeout that is not a duration, or has no time in it, stops the boot. Zero
// is not read as "no timeout": an unbounded call is what the setting exists to
// prevent, so there is no spelling of the setting that asks for one.
func TestAnUnusableCallTimeoutFailsAtBoot(t *testing.T) {
	for name, value := range map[string]string{
		"not a duration": "soon",
		"no unit":        "10",
		"empty":          "",
		"zero":           "0s",
		"negative":       "-1s",
	} {
		t.Run(name, func(t *testing.T) {
			if cfg, err := config.Load(config.Sources{Args: []string{"-call-timeout", value}}); err == nil {
				t.Errorf("Load with call-timeout %q = %+v, want an error", value, cfg)
			}
		})
	}
}
