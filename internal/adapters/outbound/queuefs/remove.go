package queuefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Remove takes the message at name out of the queue without anyone consuming
// it. A name that is only a prefix is not there to remove: no directory exists
// apart from the messages under it, and removing those is what RemoveAll is
// for.
func (f *FS) Remove(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
	case atMessage:
		if err := f.journal.Drop(ctx, key); err != nil {
			return fmt.Errorf("remove %q: %w", name, err)
		}
		return nil
	case atRoot, atQueue:
		return fmt.Errorf("remove %q: the mount itself: %w", name, fs.ErrInvalid)
	default:
		return f.refuse("remove", name, spot)
	}
}

// RemoveAll takes name and everything under it out of the queue. A name that is
// not there is not an error, and the queue itself is emptied rather than
// removed, which is how a queue is purged.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
	case atQueue, atMessage:
		if err := f.journal.DropAll(ctx, prefixOf(key)); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
		if spot == atMessage {
			if err := f.journal.Drop(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove_all %q: %w", name, err)
			}
		}
		return nil
	default:
		return f.refuse("remove_all", name, spot)
	}
}
