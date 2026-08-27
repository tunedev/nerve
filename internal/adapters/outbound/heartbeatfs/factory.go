package heartbeatfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a pulse.
const Kind = "heartbeatfs"

// IntervalSetting names how long a beat lasts, spelled as a Go duration. It is
// public: how often a watchdog can expect the count to rise is what an operator
// has to be able to read back.
const IntervalSetting = "interval"

// DefaultInterval is how long a beat lasts when a mount names no interval.
const DefaultInterval = time.Second

// Factory builds one pulse against the wall clock. An interval that is zero or
// negative is refused rather than defaulted: it would divide the elapsed time
// by nothing, and a mount that cannot beat is not a heartbeat.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	interval := DefaultInterval
	if spelled, named := settings[IntervalSetting]; named {
		parsed, err := time.ParseDuration(spelled)
		if err != nil {
			return nil, fmt.Errorf("kind %q setting %q = %q: %w", Kind, IntervalSetting, spelled, fs.ErrInvalid)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is not a beat: %w", Kind, IntervalSetting, spelled, fs.ErrInvalid)
		}
		interval = parsed
	}
	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return key == IntervalSetting }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %q, and was given %s: %w", Kind, IntervalSetting, strings.Join(extra, ", "), fs.ErrInvalid)
	}
	return New(interval, time.Now), nil
}
