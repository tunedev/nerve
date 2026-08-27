package kvfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Remove deletes the key at name. A prefix is not a key, so removing one is
// removing nothing: what is under it is RemoveAll's business.
func (f *FS) Remove(ctx context.Context, name string) error {
	key, err := f.key(name)
	if err != nil {
		return fmt.Errorf("remove %w", err)
	}

	if err := f.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	return nil
}

// RemoveAll deletes the key at name and every key under it. A path that is not
// there is not an error, and the root has no key of its own, so RemoveAll of it
// empties the namespace rather than failing on it.
//
// The listing is asked again until it answers with nothing. A store is allowed
// to hide a key while a shorter key of the same name is still there, so one
// listing is not the whole of what is under a prefix, and a delete of one
// listing's worth would leave the hidden key in the store.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if key, err := f.key(name); err == nil {
		if err := f.delete(ctx, key); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
	}

	for {
		found, err := f.store.List(ctx, f.prefix(name))
		if err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
		if len(found) == 0 {
			return nil
		}

		for _, one := range found {
			if err := f.delete(ctx, one.Key); err != nil {
				return fmt.Errorf("remove_all %q: %w", name, err)
			}
		}
	}
}

// delete removes a key that a listing said was there. A key deleted by someone
// else between the listing and the delete is the outcome asked for, so it is
// not an error.
func (f *FS) delete(ctx context.Context, key string) error {
	if err := f.store.Delete(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%q: %w", key, err)
	}
	return nil
}
