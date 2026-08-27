package kvfs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the key at oldPath to newPath, replacing what is there. It moves
// one key: a prefix is not a key, and the keys under one are not a subtree that
// moves with it.
//
// The value keeps the instant it was written. A rename does not change content,
// and a store that moves a value without its content passing through here has
// nothing to stamp a new one from.
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

	if err := f.store.Move(ctx, from, to); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	return nil
}

// Chmod sets the permission bits of the key at name. There is no directory bit
// to leave alone: a key is never a directory.
//
// It is a read and a write. The port has no verb for a mode on its own, so the
// value comes back to be stored again under the new bits — which a store that
// keeps metadata apart from content pays for and a map does not.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	key, err := f.key(name)
	if err != nil {
		return fmt.Errorf("chmod %w", err)
	}

	value, err := f.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	if err := f.store.Put(ctx, key, value.Data, mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	return nil
}
