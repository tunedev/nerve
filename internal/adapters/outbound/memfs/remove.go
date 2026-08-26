package memfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Remove deletes name. It fails on a directory that is not empty, because
// deleting what is inside one is RemoveAll's business and doing it here would
// make a typo destructive.
func (f *FS) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	holder, base, err := f.parent(name)
	if err != nil {
		return fmt.Errorf("remove %w", err)
	}

	found, present := holder.children[base]
	if !present {
		return fmt.Errorf("remove %q: %w", name, fs.ErrNotExist)
	}
	if found.isDir() && len(found.children) > 0 {
		return fmt.Errorf("remove %q: %d entries are still in it: %w", name, len(found.children), fs.ErrExist)
	}

	delete(holder.children, base)
	return nil
}

// RemoveAll deletes name and everything beneath it. A path that is not there is
// not an error. The root is the one path that cannot be deleted, so it is
// emptied instead: everything beneath it is what the verb promises either way.
func (f *FS) RemoveAll(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(elements(name)) == 0 {
		clear(f.root.children)
		return nil
	}

	holder, base, err := f.parent(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove_all %w", err)
	}

	delete(holder.children, base)
	return nil
}
