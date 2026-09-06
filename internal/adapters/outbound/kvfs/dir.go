package kvfs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without reading it. It answers for keys and never for the
// prefixes above them: a prefix exists only because keys are under it, so there
// is nothing at one to describe.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	key, err := f.key(name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %w", err)
	}

	one, err := f.store.Head(ctx, key)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	return info(name, one), nil
}

// ReadDir lists what is directly under the prefix name, sorted by name. It does
// not refuse a name nothing is under, because a name is a prefix: listing one
// that holds nothing is how a caller finds out that it does.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	under := f.prefix(name)
	found, err := f.store.List(ctx, under)
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, err)
	}

	children := make(map[string]vfs.FileInfo, len(found))
	for _, one := range found {
		listed := vfs.FileInfo{Size: one.Size, Mode: one.Mode, ModTime: one.ModTime}
		keyspace.Fold(children, strings.TrimPrefix(one.Key, under), listed)
	}

	entries := slices.Collect(maps.Values(children))
	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}
