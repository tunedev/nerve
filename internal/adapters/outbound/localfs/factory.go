package localfs

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a host directory.
const Kind = "localfs"

// RootSetting names the host directory the mount stands for. It is the one
// setting this kind takes, and the composition root declares it public: a
// directory path is what an operator has to be able to read back to know which
// disk a mount is on, and no credential is spelled in it.
const RootSetting = "root"

// Factory builds one mount over a host directory that is already there. A root
// that is missing, or is not a directory, is a boot failure rather than a
// directory this quietly creates: the mount stands for a disk somebody else
// manages, and a typo that made an empty one would look like a working mount
// over an empty disk.
//
// Any other setting is refused rather than ignored, for the reason a misspelled
// one is worth a boot failure: it is a setting meant for another kind, and boot
// is the last place an operator can still act on it.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	root, named := settings[RootSetting]
	if !named {
		return nil, fmt.Errorf("kind %q needs a %q setting naming a host directory: %w", Kind, RootSetting, fs.ErrInvalid)
	}
	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return key == RootSetting }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %q, and was given %s: %w", Kind, RootSetting, strings.Join(extra, ", "), fs.ErrInvalid)
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("kind %q root %q: %w", Kind, root, err)
	}
	found, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("kind %q root: %w", Kind, err)
	}
	if !found.IsDir() {
		return nil, fmt.Errorf("kind %q root %q is not a directory: %w", Kind, absolute, fs.ErrInvalid)
	}

	fsys, err := New(absolute)
	if err != nil {
		return nil, fmt.Errorf("kind %q root: %w", Kind, err)
	}
	return fsys, nil
}
