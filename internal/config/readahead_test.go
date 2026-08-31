package config_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// A checkout with no config of its own reads ahead, because the bottom layer
// names a window like it names everything else.
func TestTheDefaultReadaheadWindowIsThePortsOwn(t *testing.T) {
	if got := config.Defaults().ReadaheadBytes(); got != vfs.DefaultReadahead {
		t.Errorf("default readahead-window resolves to %d, want the port's %d", got, vfs.DefaultReadahead)
	}
}

// The setting arrives as text and leaves as bytes, so the registry that builds
// the mounts never parses anything.
func TestTheReadaheadWindowResolvesToBytes(t *testing.T) {
	cfg, err := config.Load(config.Sources{Args: []string{"-readahead-window", "65536"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.ReadaheadBytes(); got != 65536 {
		t.Errorf("ReadaheadBytes() = %d, want %d", got, 65536)
	}
}

// Zero is how an operator turns reading ahead off, so it has to boot. Everything
// else that is not a count of bytes does not.
func TestZeroTurnsReadingAheadOffAndNonsenseFailsAtBoot(t *testing.T) {
	cfg, err := config.Load(config.Sources{Args: []string{"-readahead-window", "0"}})
	if err != nil {
		t.Fatalf("Load with readahead-window 0: %v", err)
	}
	if got := cfg.ReadaheadBytes(); got != 0 {
		t.Errorf("ReadaheadBytes() = %d, want 0", got)
	}

	for name, value := range map[string]string{
		"not a number": "lots",
		"empty":        "",
		"negative":     "-1",
		"a size":       "256KiB",
	} {
		t.Run(name, func(t *testing.T) {
			if cfg, err := config.Load(config.Sources{Args: []string{"-readahead-window", value}}); err == nil {
				t.Errorf("Load with readahead-window %q = %+v, want an error", value, cfg)
			}
		})
	}
}
