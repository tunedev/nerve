package streamfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for append-only streams.
const Kind = "streamfs"

// The settings one mount takes. Both are public: how much of a stream is kept
// and how long a reader waits for the next append are what an operator has to
// be able to read back, because between them they are the whole of what a
// stream mount promises.
const (
	CapacitySetting = "capacity"
	FollowSetting   = "follow"
)

// DefaultCapacity is how many bytes of a stream are kept when a mount names no
// capacity, and DefaultFollow is how long a reader waits at the end of a stream
// for the next append when a mount names no follow.
const (
	DefaultCapacity = 64 << 10
	DefaultFollow   = 30 * time.Second
)

// Public names the settings safe to read back to an operator, which is both of
// them: a stream mount holds no credential.
var Public = []string{CapacitySetting, FollowSetting}

// Factory builds one set of streams. A capacity that is zero or negative is
// refused rather than defaulted: a ring that holds nothing drops every byte the
// moment it is written, and a stream nothing can be read from is not a stream.
//
// A negative follow is refused for the same reason a capacity is. Zero is not:
// it is a mount whose readers end as soon as they have caught up, which is what
// a caller reading what is there rather than what arrives next asks for.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	if extra := unknownSettings(settings); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %s, and was given %s: %w",
			Kind, strings.Join(Public, ", "), strings.Join(extra, ", "), fs.ErrInvalid)
	}

	capacity := DefaultCapacity
	if spelled, named := settings[CapacitySetting]; named {
		parsed, err := strconv.Atoi(spelled)
		if err != nil {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is not a count of bytes: %w", Kind, CapacitySetting, spelled, fs.ErrInvalid)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is a ring that keeps nothing: %w", Kind, CapacitySetting, spelled, fs.ErrInvalid)
		}
		capacity = parsed
	}

	follow := DefaultFollow
	if spelled, named := settings[FollowSetting]; named {
		parsed, err := time.ParseDuration(spelled)
		if err != nil {
			return nil, fmt.Errorf("kind %q setting %q = %q: %w", Kind, FollowSetting, spelled, fs.ErrInvalid)
		}
		if parsed < 0 {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is a wait that is over before it starts: %w", Kind, FollowSetting, spelled, fs.ErrInvalid)
		}
		follow = parsed
	}

	return New(capacity, follow), nil
}

// unknownSettings is what a mount named that this kind does not take, which is
// what makes a misspelling a boot failure instead of a silently ignored line.
func unknownSettings(settings map[string]string) []string {
	return slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(named string) bool {
		return slices.Contains(Public, named)
	})
}
