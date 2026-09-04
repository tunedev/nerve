package vectorfs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the document at oldPath to newPath, replacing the document
// there.
//
// The vector travels with it and is not made again: a rename changes where a
// document is filed and not what it says, so paying a model call for it would
// buy the same numbers.
//
// It moves one document rather than a subtree: a prefix is not an entry, so
// there is nothing under a name to bring along. Only documents move — the
// mount's own three paths are its shape, not entries a caller can rearrange.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	source, from := locate(oldPath)
	landing, to := locate(newPath)

	if fixed(source) || fixed(landing) {
		return fmt.Errorf("rename %q to %q: the mount itself: %w", oldPath, newPath, fs.ErrInvalid)
	}
	if source != atDoc {
		return f.refuse("rename", oldPath, source)
	}
	if landing != atDoc {
		return f.refuse("rename", newPath, landing)
	}

	if from == to {
		if _, err := f.index.Peek(ctx, from); err != nil {
			return fmt.Errorf("rename %q: %w", oldPath, err)
		}
		return nil
	}
	if err := f.index.Move(ctx, from, to); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	return nil
}

// Chmod sets the permission bits the document at name carries. An index has no
// modes of its own, so the bits travel with the document the way its body does.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	spot, key := locate(name)
	if spot != atDoc {
		return f.refuse("chmod", name, spot)
	}

	if err := f.index.Chmod(ctx, key, mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", name, err)
	}
	return nil
}

// fixed reports whether a path is one of the mount's own three, which are its
// shape and not entries a verb may move.
func fixed(where spot) bool { return where == atRoot || where == atCorpus || where == atQuery }
