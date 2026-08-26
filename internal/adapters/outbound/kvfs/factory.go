package kvfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a key-value mount.
const Kind = "kvfs"

// NamespaceSetting names the part of the keyspace a mount holds. It is the whole
// of a kvfs mount's config, and it is not a secret: an operator reads it back to
// know which keys a mount is over.
const NamespaceSetting = "namespace"

// Factory builds mounts over one store, which is what makes a namespace mean
// anything: the composition root owns the keyspace, and every mount from this
// factory is a part of it.
//
// The namespace is required rather than defaulted. A mount without one holds the
// whole keyspace, and two of those over one store would answer each other's
// keys — a collision an operator would only find as missing session state.
func Factory(store Store) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		namespace, named := settings[NamespaceSetting]
		if !named {
			return nil, fmt.Errorf("kind %q needs the %q setting, naming the part of the keyspace this mount holds: %w", Kind, NamespaceSetting, fs.ErrInvalid)
		}
		if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return key == NamespaceSetting }); len(extra) > 0 {
			return nil, fmt.Errorf("kind %q takes only %q, and was given %s: %w", Kind, NamespaceSetting, strings.Join(extra, ", "), fs.ErrInvalid)
		}
		if namespaceKey(namespace) == "" {
			return nil, fmt.Errorf("kind %q was given the empty namespace %q, which is the whole keyspace: %w", Kind, namespace, fs.ErrInvalid)
		}
		return New(store, namespace), nil
	}
}
