package kvfs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

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

	value, err := f.store.Get(ctx, key)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	return info(name, value), nil
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
		fold(children, strings.TrimPrefix(one.Key, under), one)
	}

	entries := slices.Collect(maps.Values(children))
	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// fold adds one listed key to what ReadDir answers. The key either names a child
// outright, or names one segment of a prefix that exists because it and others
// are under it.
//
// A key and a prefix can share a name, and the key is the one that wins: it
// describes itself, where a prefix is only ever described by what is beneath it.
func fold(children map[string]vfs.FileInfo, rest string, one Entry) {
	base, _, deeper := strings.Cut(rest, "/")
	if !deeper {
		children[base] = vfs.FileInfo{Name: base, Size: one.Size, Mode: one.Mode, ModTime: one.ModTime}
		return
	}

	existing, listed := children[base]
	if listed && !existing.IsDir() {
		return
	}
	if one.ModTime.After(existing.ModTime) {
		existing = vfs.FileInfo{Name: base, Mode: dirMode, ModTime: one.ModTime}
	}
	children[base] = existing
}
