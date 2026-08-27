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
// however deep: an object store has no subtree to walk, only longer names. It
// is asked again until it answers with nothing, because a store is allowed to
// hide a key while a shorter key of the same name is still there, and a delete
// of one listing's worth would leave that key in the bucket.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if !isRoot(name) {
		if err := f.delete(ctx, key(name)); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
	}

	for {
		listing, err := f.bucket.List(ctx, prefixOf(name), "")
		if err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
		if len(listing.Objects) == 0 {
			return nil
		}

		for _, object := range listing.Objects {
			if err := f.delete(ctx, object.Key); err != nil {
				return fmt.Errorf("remove_all %q: %w", name, err)
			}
		}
	}
}

// delete removes an object a listing said was there. An object deleted by
// someone else between the listing and the delete is the outcome asked for, so
// it is not an error.
func (f *FS) delete(ctx context.Context, doomed string) error {
	if err := f.bucket.Delete(ctx, doomed); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%q: %w", doomed, err)
	}
	return nil
}
