package localfs

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without reading it.
func (f *FS) Stat(_ context.Context, name string) (vfs.FileInfo, error) {
	found, err := os.Stat(f.host(name))
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, invalid(err))
	}
	return info(name, found), nil
}

// ReadDir lists the direct children of name, sorted by name.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	children, err := os.ReadDir(f.host(name))
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, invalid(err))
	}

	entries := make([]vfs.FileInfo, 0, len(children))
	for _, child := range children {
		described, err := child.Info()
		if err != nil {
			return nil, fmt.Errorf("read_dir %q: %w", name, invalid(err))
		}
		entries = append(entries, info(path.Join(name, child.Name()), described))
	}
	return entries, nil
}
