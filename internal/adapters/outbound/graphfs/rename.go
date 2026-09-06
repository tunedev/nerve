package graphfs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the text at oldPath to newPath, replacing the text there.
//
// Nothing is extracted again: a rename changes where a text is filed and not
// what it says, so the entities and relations travel with it unchanged and the
// graph a caller expands is the same graph before and after.
//
// It moves one text rather than a subtree: a prefix is not an entry, so there
// is nothing under a name to bring along. Only texts move — the mount's own
// three paths are its shape, not entries a caller can rearrange.
//
// Unlike an index over two stores, there is no order here to get wrong. The
// move and everything derived from it are one change to one blob, so it either
// happened or it did not.
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

	return f.commit(ctx, func(documents map[string]document) error {
		held, there := documents[from]
		if !there {
			return fmt.Errorf("rename %q: %w", oldPath, fs.ErrNotExist)
		}
		if from == to {
			return nil
		}
		documents[to] = held
		delete(documents, from)
		return nil
	})
}

// Chmod sets the permission bits the text at name carries. It changes nothing
// about the graph: a mode is part of the text and says nothing about what the
// text named.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	spot, key := locate(name)
	if spot != atDoc {
		return f.refuse("chmod", name, spot)
	}

	return f.commit(ctx, func(documents map[string]document) error {
		held, there := documents[key]
		if !there {
			return fmt.Errorf("chmod %q: %w", name, fs.ErrNotExist)
		}
		held.Mode = mode.Perm()
		documents[key] = held
		return nil
	})
}

// fixed reports whether a path is one of the mount's own three, which are its
// shape and not entries a verb may move.
func fixed(where spot) bool { return where == atRoot || where == atCorpus || where == atNeighbours }
