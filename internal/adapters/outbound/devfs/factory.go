package devfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for device entries.
const Kind = "devfs"

// SizeSetting names how many bytes a read of a source hands back. It is public:
// how much a read costs is what an operator has to be able to read back.
const SizeSetting = "size"

// DefaultSize is what a source hands back when a mount names no size.
const DefaultSize = 4096

// Factory builds one device mount. A size that is zero or negative is refused
// rather than defaulted: a source that hands back nothing is the sink, and
// there is already one of those.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	size := DefaultSize
	if spelled, named := settings[SizeSetting]; named {
		parsed, err := strconv.Atoi(spelled)
		if err != nil {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is not a count of bytes: %w", Kind, SizeSetting, spelled, fs.ErrInvalid)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is a source that hands back nothing: %w", Kind, SizeSetting, spelled, fs.ErrInvalid)
		}
		size = parsed
	}
	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return key == SizeSetting }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %q, and was given %s: %w", Kind, SizeSetting, strings.Join(extra, ", "), fs.ErrInvalid)
	}
	return New(size), nil
}
