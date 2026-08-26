package objectfs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the object at oldPath to newPath, replacing what is there.
//
// It moves one object rather than a subtree: a prefix is not an entry, so there
// is nothing under a name to bring along. The copy is the bucket's, so the
// content never travels through this process to move across it.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	if isRoot(oldPath) || isRoot(newPath) {
		return fmt.Errorf("rename %q to %q: the bucket itself: %w", oldPath, newPath, fs.ErrInvalid)
	}

	from, to := key(oldPath), key(newPath)
	object, err := f.bucket.Head(ctx, from)
	if err != nil {
		return fmt.Errorf("rename %q: %w", oldPath, err)
	}
	if from == to {
		return nil
	}

	if err := f.bucket.Copy(ctx, from, to, object.Mode.Perm()); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	if err := f.bucket.Delete(ctx, from); err != nil {
		return fmt.Errorf("rename %q to %q: the copy landed, %q is still there: %w", oldPath, newPath, oldPath, err)
	}
	return nil
}

// Chmod sets the permission bits of name.
//
// An object store has no modes, so the bits are metadata beside the content and
// setting them is a copy onto the same key. Only the permission bits are taken:
// nothing stored under a key is a directory.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	if isRoot(name) {
		return fmt.Errorf("chmod %q: the bucket itself: %w", name, fs.ErrInvalid)
	}

	if _, err := f.bucket.Head(ctx, key(name)); err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	if err := f.bucket.Copy(ctx, key(name), key(name), mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	return nil
}
