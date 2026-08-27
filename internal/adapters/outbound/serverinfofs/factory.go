package serverinfofs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask the process about itself.
const Kind = "serverinfofs"

// Factory builds mounts describing this process, built at version. The version
// is a parameter rather than a setting because it is the binary's, not the
// operator's: a build that let a config say which version it was would be a
// build that could lie about itself.
func Factory(version string) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		if len(settings) > 0 {
			named := slices.Sorted(maps.Keys(settings))
			return nil, fmt.Errorf("kind %q takes no settings, and was given %s: %w", Kind, strings.Join(named, ", "), fs.ErrInvalid)
		}
		return New(version, time.Now), nil
	}
}
