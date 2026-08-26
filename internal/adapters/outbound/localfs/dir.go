package localfs

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without reading it.
func (f *FS) Stat(_ context.Context, name string) (vfs.FileInfo, error) {
	found, err := f.root.Stat(rel(name))
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, sentinel(err))
	}
	return info(name, found), nil
}

// ReadDir lists the direct children of name, sorted by name.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	children, err := f.children(name)
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, err)
	}

	entries := make([]vfs.FileInfo, 0, len(children))
	for _, child := range children {
		described, err := child.Info()
		if err != nil {
			return nil, fmt.Errorf("read_dir %q: %w", name, sentinel(err))
		}
		entries = append(entries, info(path.Join(name, child.Name()), described))
	}
	return entries, nil
}

// children lists what the host holds directly under name, sorted by name. An
// os.Root opens a directory rather than listing one, so the listing is taken
// through the handle containment is enforced on: that handle is closed here
// rather than handed out, and it reports entries in the order the host holds
// them rather than in the order the port promises.
func (f *FS) children(name string) ([]os.DirEntry, error) {
	dir, err := f.root.Open(rel(name))
	if err != nil {
		return nil, sentinel(err)
	}
	defer func() { _ = dir.Close() }()

	listed, err := dir.ReadDir(-1)
	if err != nil {
		return nil, sentinel(err)
	}
	slices.SortFunc(listed, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return listed, nil
}
