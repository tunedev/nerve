package streamrotatefs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for rotating logs.
const Kind = "streamrotatefs"

// The settings one mount takes. Both are public: how large a segment grows and
// how many are kept are what an operator has to be able to read back, because
// between them they are the whole of what a rotating mount promises and the
// bound on how much it holds.
const (
	SizeSetting = "size"
	KeepSetting = "keep"
)

// DefaultSize is how many bytes a segment grows to before it is sealed when a
// mount names no size, and DefaultKeep is how many sealed segments of a log are
// kept when a mount names no keep.
const (
	DefaultSize = 1 << 20
	DefaultKeep = 4
)

// Public names the settings safe to read back to an operator, which is both of
// them: a rotating mount holds no credential.
var Public = []string{KeepSetting, SizeSetting}

// Factory builds one set of rotating logs. A size that is zero or negative is
// refused rather than defaulted: a segment sealed the moment it is written to
// is a log of empty segments, and nothing can be read out of one.
//
// A negative keep is refused for the same reason. Zero is not: it is a mount
// that rotates and discards, which is a log bounded to one segment and asked
// for by anyone who wants the recent part of a log and none of the rest.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	if extra := unknownSettings(settings); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %s, and was given %s: %w",
			Kind, strings.Join(Public, ", "), strings.Join(extra, ", "), fs.ErrInvalid)
	}

	size, err := count(settings, SizeSetting, DefaultSize)
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		return nil, fmt.Errorf("kind %q setting %q = %q, which is a segment sealed before anything is in it: %w",
			Kind, SizeSetting, settings[SizeSetting], fs.ErrInvalid)
	}

	keep, err := count(settings, KeepSetting, DefaultKeep)
	if err != nil {
		return nil, err
	}
	if keep < 0 {
		return nil, fmt.Errorf("kind %q setting %q = %q, which is fewer segments than none: %w",
			Kind, KeepSetting, settings[KeepSetting], fs.ErrInvalid)
	}

	return New(size, keep), nil
}

// count reads a whole number a mount named, or hands back the default when it
// named none.
func count(settings map[string]string, named string, fallback int) (int, error) {
	spelled, there := settings[named]
	if !there {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(spelled)
	if err != nil {
		return 0, fmt.Errorf("kind %q setting %q = %q, which is not a whole number: %w", Kind, named, spelled, fs.ErrInvalid)
	}
	return parsed, nil
}

// unknownSettings is what a mount named that this kind does not take, which is
// what makes a misspelling a boot failure instead of a silently ignored line.
func unknownSettings(settings map[string]string) []string {
	return slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(named string) bool {
		return slices.Contains(Public, named)
	})
}
