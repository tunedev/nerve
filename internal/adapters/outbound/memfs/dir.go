package memfs

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without reading it.
func (f *FS) Stat(_ context.Context, name string) (vfs.FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	found, err := f.walk(name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %w", err)
	}
	return found.info(), nil
}

// ReadDir lists the direct children of name, sorted by name.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	found, err := f.walk(name)
	if err != nil {
		return nil, fmt.Errorf("read_dir %w", err)
	}
	if !found.isDir() {
		return nil, fmt.Errorf("read_dir %q: not a directory: %w", name, fs.ErrInvalid)
	}

	entries := make([]vfs.FileInfo, 0, len(found.children))
	for _, child := range found.children {
		entries = append(entries, child.info())
	}
	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}
