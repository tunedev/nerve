package queuefs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the message at oldPath to newPath, replacing the message queued
// there.
//
// It moves one message rather than a subtree: a prefix is not an entry, so
// there is nothing under a name to bring along. Only messages move — the
// mount's own two paths are its shape, not entries a caller can rearrange.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	source, from := locate(oldPath)
	landing, to := locate(newPath)

	if source == atRoot || source == atQueue || landing == atRoot || landing == atQueue {
		return fmt.Errorf("rename %q to %q: the mount itself: %w", oldPath, newPath, fs.ErrInvalid)
	}
	if source != atMessage {
		return f.refuse("rename", oldPath, source)
	}
	if landing != atMessage {
		return f.refuse("rename", newPath, landing)
	}

	if from == to {
		if _, err := f.journal.Peek(ctx, from); err != nil {
			return fmt.Errorf("rename %q: %w", oldPath, err)
		}
		return nil
	}
	if err := f.journal.Move(ctx, from, to); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	return nil
}

// Chmod sets the permission bits the message at name carries. A queue has no
// modes of its own, so the bits travel with the message the way its body does.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	spot, key := locate(name)
	if spot != atMessage {
		return f.refuse("chmod", name, spot)
	}

	if err := f.journal.Chmod(ctx, key, mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	return nil
}
