package localfs

import (
	"context"
	"fmt"
	"os"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsSymlinker reports that one path on a host disk can stand for another. This
// is the verb set memfs refuses: a tree in memory would have to invent what a
// link means, and here the host has already decided.
func (f *FS) AsSymlinker() vfs.Symlinker { return f }

// Symlink makes newPath stand for oldPath, which need not exist. The link is
// written with the host path of its target, so the host resolves it the same
// way this mount does.
func (f *FS) Symlink(_ context.Context, oldPath, newPath string) error {
	if err := os.Symlink(f.host(oldPath), f.host(newPath)); err != nil {
		return fmt.Errorf("symlink %q to %q: %w", oldPath, newPath, invalid(err))
	}
	return nil
}

// Readlink returns what path stands for, in the mount's vocabulary rather than
// the host's.
func (f *FS) Readlink(_ context.Context, name string) (string, error) {
	target, err := os.Readlink(f.host(name))
	if err != nil {
		return "", fmt.Errorf("readlink %q: %w", name, invalid(err))
	}
	return f.mount(target), nil
}
