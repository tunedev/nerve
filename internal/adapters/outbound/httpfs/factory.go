package httpfs

import (
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for an HTTP origin.
const Kind = "httpfs"

// The settings this kind takes. The origin is where the mount points; the bound
// is how much of a response it will hand back.
const (
	BaseSetting     = "base"
	MaxBytesSetting = "max_bytes"
)

// DefaultMaxBytes is how much a read hands back when a mount names no bound.
const DefaultMaxBytes = 1 << 20

// Public names the settings this kind reads back to an operator. Both are
// operational rather than secret: which origin a mount points at, and how much
// of it a read will take. A credential belongs in the origin's own auth, not in
// a mount setting, so there is none here to redact.
var Public = []string{BaseSetting, MaxBytesSetting}

// Factory builds one mount over an origin an operator names. It shares no
// client between mounts: connection pooling is per origin, and one mount's
// slow host should not be holding another's connections.
func Factory(settings map[string]string) (vfs.FileSystem, error) {
	base, named := settings[BaseSetting]
	if !named {
		return nil, fmt.Errorf("kind %q needs a %q setting naming an origin: %w", Kind, BaseSetting, fs.ErrInvalid)
	}

	max := int64(DefaultMaxBytes)
	if spelled, bounded := settings[MaxBytesSetting]; bounded {
		parsed, err := strconv.ParseInt(spelled, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("kind %q setting %q = %q, which is not a count of bytes: %w", Kind, MaxBytesSetting, spelled, fs.ErrInvalid)
		}
		max = parsed
	}

	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return slices.Contains(Public, key) }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %s, and was given %s: %w", Kind, strings.Join(Public, ", "), strings.Join(extra, ", "), fs.ErrInvalid)
	}

	fsys, err := New(base, max, &http.Client{})
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	return fsys, nil
}
