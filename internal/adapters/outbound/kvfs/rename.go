package kvfs

import (
	"context"
	"fmt"
	"io/fs"
	"time"
)

// Rename moves the key at oldPath to newPath, replacing what is there. It moves
// one key: a prefix is not a key, and the keys under one are not a subtree that
// moves with it.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	from, err := f.key(oldPath)
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	to, err := f.key(newPath)
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	if from == to {
		return nil
	}

	value, err := f.store.Get(ctx, from)
	if err != nil {
		return fmt.Errorf("rename %q: %w", oldPath, err)
	}
	value.ModTime = time.Now()
	if err := f.store.Put(ctx, to, value); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	if err := f.store.Delete(ctx, from); err != nil {
		return fmt.Errorf("rename %q: %w", oldPath, err)
	}
	return nil
}

// Chmod sets the permission bits of the key at name. There is no directory bit
// to leave alone: a key is never a directory.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	key, err := f.key(name)
	if err != nil {
		return fmt.Errorf("chmod %w", err)
	}

	value, err := f.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	value.Mode = mode.Perm()
	value.ModTime = time.Now()

	if err := f.store.Put(ctx, key, value); err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	return nil
}
