package localfs

import (
	"context"
	"fmt"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsSymlinker reports that one path on a host disk can stand for another. This
// is the verb set memfs refuses: a tree in memory would have to invent what a
// link means, and here the host has already decided.
func (f *FS) AsSymlinker() vfs.Symlinker { return f }

// Symlink makes newPath stand for oldPath, which need not exist. The target is
// written relative to the directory holding the link, so it resolves inside the
// root: an absolute host path would name a location the root refuses to follow,
// and a link this mount made is one this mount reads through.
func (f *FS) Symlink(_ context.Context, oldPath, newPath string) error {
	target := relative(path.Dir(path.Join("/", newPath)), oldPath)
	if err := f.root.Symlink(target, rel(newPath)); err != nil {
		return fmt.Errorf("symlink %q to %q: %w", oldPath, newPath, sentinel(err))
	}
	return nil
}

// Readlink returns what path stands for, in the mount's vocabulary rather than
// the host's.
func (f *FS) Readlink(_ context.Context, name string) (string, error) {
	target, err := f.root.Readlink(rel(name))
	if err != nil {
		return "", fmt.Errorf("readlink %q: %w", name, sentinel(err))
	}
	return linkTarget(path.Dir(path.Join("/", name)), target), nil
}

// Lstat describes name without following a link at it, which is what the host
// spells Lstat and what os.Root resolves without leaving the root.
func (f *FS) Lstat(_ context.Context, name string) (vfs.FileInfo, error) {
	found, err := f.root.Lstat(rel(name))
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("lstat %q: %w", name, sentinel(err))
	}
	return info(name, found), nil
}
