package vectorfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Remove takes the document at name out of the corpus, and its vector with it.
// A name that is only a prefix is not there to remove: no directory exists
// apart from the documents under it, and removing those is what RemoveAll is
// for.
//
// The vector goes first and the document second, which is the order that never
// leaves an answer naming a document nobody can read. The corpus is what
// reports a name that was not there, because the corpus is what says a document
// is there at all.
func (f *FS) Remove(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
	case atDoc:
		if err := f.index.Drop(ctx, key); err != nil {
			return fmt.Errorf("remove %q: %w", name, err)
		}
		if err := f.docs.Delete(ctx, f.doc(key)); err != nil {
			return fmt.Errorf("remove %q: %w", name, err)
		}
		return nil
	case atRoot, atCorpus, atQuery:
		return fmt.Errorf("remove %q: the mount itself: %w", name, fs.ErrInvalid)
	default:
		return f.refuse("remove", name, spot)
	}
}

// RemoveAll takes name and everything under it out of the corpus. A name that
// is not there is not an error, and the corpus itself is emptied rather than
// removed, which is how an index is cleared.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
	case atCorpus, atDoc:
		if err := f.index.DropAll(ctx, prefixOf(key)); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
		if err := f.docs.DeleteAll(ctx, f.doc(prefixOf(key))); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
		if spot == atDoc {
			if err := f.index.Drop(ctx, key); err != nil {
				return fmt.Errorf("remove_all %q: %w", name, err)
			}
			if err := f.docs.Delete(ctx, f.doc(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove_all %q: %w", name, err)
			}
		}
		return nil
	default:
		return f.refuse("remove_all", name, spot)
	}
}
