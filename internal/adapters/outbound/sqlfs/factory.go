package sqlfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a tree in SQLite.
const Kind = "sqlfs"

// PathSetting names the database file the mount stores its tree in. It is the
// one setting sqlfs takes, and it is required: a SQLite mount with nowhere to
// write is a tree in memory with a driver in the way, which memfs already is.
const PathSetting = "path"

// Factory builds one tree per mount from the database file its config names. It
// refuses a setting it cannot honour rather than ignoring one, because a
// setting sqlfs cannot use is a misspelling of one meant for another kind, and
// boot is the last place an operator can still act on it.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	file := settings[PathSetting]
	if file == "" {
		return nil, fmt.Errorf("kind %q needs a %q setting naming its database file: %w", Kind, PathSetting, fs.ErrInvalid)
	}

	if extra := slices.Sorted(maps.Keys(settings)); len(extra) > 1 {
		named := slices.DeleteFunc(extra, func(key string) bool { return key == PathSetting })
		return nil, fmt.Errorf("kind %q takes only %q, and was also given %s: %w", Kind, PathSetting, strings.Join(named, ", "), fs.ErrInvalid)
	}

	fsys, err := Open(file)
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	return fsys, nil
}
