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
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	found, err := f.store.List(ctx, f.prefix(name))
	if err != nil {
		return fmt.Errorf("remove_all %q: %w", name, err)
	}

	keys := make([]string, 0, len(found)+1)
	for _, one := range found {
		keys = append(keys, one.Key)
	}
	if key, err := f.key(name); err == nil {
		keys = append(keys, key)
	}

	for _, key := range keys {
		if err := f.store.Delete(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
	}
	return nil
}
