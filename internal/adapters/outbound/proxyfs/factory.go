package proxyfs

import (
	"fmt"
	"io/fs"
	"maps"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a forwarding mount.
const Kind = "proxyfs"

// The settings this kind takes. Everything else a mount names is passed through
// to the kind behind it, which is what makes a proxy over any kind a line of
// config rather than a new plugin.
const (
	KindSetting   = "target_kind"
	PrefixSetting = "target_prefix"
)

// Public names the settings this kind reads back to an operator: which kind is
// behind the proxy, and which of its subtrees. The pass-through settings are
// the target kind's, and read back redacted, because only the kind a registry
// was asked for gets to say which of its settings is a credential and a proxy
// cannot say it on another kind's behalf.
var Public = []string{KindSetting, PrefixSetting}

// Factory builds a proxy over a target it asks build for. build is the
// registry's bare build rather than its wrapping one: the proxy itself is
// wrapped on the way into the table, and a target wrapped a second time would
// be retried by two layers.
//
// A proxy over a proxy is refused. Nothing useful is behind the second one that
// is not behind the first, and a config naming itself would build until the
// stack ran out.
func Factory(build func(vfs.MountSpec) (vfs.FileSystem, error)) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		kind, named := settings[KindSetting]
		if !named {
			return nil, fmt.Errorf("kind %q needs a %q setting naming the kind behind it: %w", Kind, KindSetting, fs.ErrInvalid)
		}
		if kind == Kind {
			return nil, fmt.Errorf("kind %q behind a %q reaches nothing a proxy over its target would not: %w", Kind, Kind, fs.ErrInvalid)
		}

		prefix := path.Join("/", settings[PrefixSetting])

		passed := maps.Clone(settings)
		delete(passed, KindSetting)
		delete(passed, PrefixSetting)

		target, err := build(vfs.MountSpec{Prefix: prefix, Kind: kind, Settings: passed})
		if err != nil {
			return nil, fmt.Errorf("kind %q over %q: %w", Kind, kind, err)
		}
		return New(target, prefix), nil
	}
}
