package objectfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Remove deletes the object at name. A name that is only a prefix is not there
// to delete: no directory exists apart from the entries under it, and deleting
// those is what RemoveAll is for.
func (f *FS) Remove(ctx context.Context, name string) error {
	if isRoot(name) {
		return fmt.Errorf("remove %q: the bucket itself: %w", name, fs.ErrInvalid)
	}

	if _, err := f.bucket.Head(ctx, key(name)); err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	if err := f.bucket.Delete(ctx, key(name)); err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	return nil
}

// RemoveAll deletes name and everything under it. A name that is not there is
// not an error, and the bucket itself is emptied rather than deleted.
//
// The listing takes no delimiter, so it reaches every key under the prefix
// however deep: an object store has no subtree to walk, only longer names.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	listing, err := f.bucket.List(ctx, prefixOf(name), "")
	if err != nil {
		return fmt.Errorf("remove_all %q: %w", name, err)
	}

	keys := make([]string, 0, len(listing.Objects)+1)
	for _, object := range listing.Objects {
		keys = append(keys, object.Key)
	}
	if !isRoot(name) {
		keys = append(keys, key(name))
	}

	for _, doomed := range keys {
		if err := f.bucket.Delete(ctx, doomed); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove_all %q: %q: %w", name, doomed, err)
		}
	}
	return nil
}
