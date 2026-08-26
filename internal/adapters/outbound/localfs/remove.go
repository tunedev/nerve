package localfs

import (
	"context"
	"fmt"
	"io/fs"
	"path"
)

// Remove deletes name. It fails on a directory that is not empty, because
// deleting what is inside one is RemoveAll's business and doing it here would
// make a typo destructive.
func (f *FS) Remove(_ context.Context, name string) error {
	if isRoot(name) {
		return fmt.Errorf("remove %q: the root: %w", name, fs.ErrInvalid)
	}
	if err := f.root.Remove(rel(name)); err != nil {
		return fmt.Errorf("remove %q: %w", name, sentinel(err))
	}
	return nil
}

// RemoveAll deletes name and everything beneath it. A path that is not there is
// not an error. The root is the one path that cannot be deleted, so it is
// emptied instead: everything beneath it is what the verb promises either way.
func (f *FS) RemoveAll(_ context.Context, name string) error {
	if !isRoot(name) {
		if err := f.root.RemoveAll(rel(name)); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, sentinel(err))
		}
		return nil
	}

	children, err := f.children(name)
	if err != nil {
		return fmt.Errorf("remove_all %q: %w", name, err)
	}
	for _, child := range children {
		if err := f.root.RemoveAll(rel(path.Join(name, child.Name()))); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, sentinel(err))
		}
	}
	return nil
}
