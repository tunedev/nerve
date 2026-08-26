package memfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a tree in memory.
const Kind = "memfs"

// Factory builds one tree per mount. It takes no settings, and refuses any it
// is given rather than ignoring them: a setting memfs cannot honour is a
// misspelling of one meant for another kind, and boot is the last place an
// operator can still act on it.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	if len(settings) > 0 {
		named := slices.Sorted(maps.Keys(settings))
		return nil, fmt.Errorf("kind %q takes no settings, and was given %s: %w", Kind, strings.Join(named, ", "), fs.ErrInvalid)
	}
	return New(), nil
}
