package memfs

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"time"
)

// Rename moves oldPath to newPath, replacing newPath if it exists. It refuses
// to replace a directory with entries in it, for the reason Remove refuses to
// delete one.
func (f *FS) Rename(_ context.Context, oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if samePath(oldPath, newPath) {
		return nil
	}
	if descends(newPath, oldPath) {
		return fmt.Errorf("rename %q to %q: a directory cannot hold itself: %w", oldPath, newPath, fs.ErrInvalid)
	}

	from, oldBase, err := f.parent(oldPath)
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	moving, present := from.children[oldBase]
	if !present {
		return fmt.Errorf("rename %q: %w", oldPath, fs.ErrNotExist)
	}

	to, newBase, err := f.parent(newPath)
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	if landing, taken := to.children[newBase]; taken && landing.isDir() && len(landing.children) > 0 {
		return fmt.Errorf("rename %q to %q: %d entries are still in it: %w", oldPath, newPath, len(landing.children), fs.ErrExist)
	}

	delete(from.children, oldBase)
	moving.name = newBase
	moving.modTime = time.Now()
	to.children[newBase] = moving
	return nil
}

// descends reports whether name is at or beneath ancestor, which is what a move
// into a directory's own subtree looks like.
func descends(name, ancestor string) bool {
	below, above := elements(name), elements(ancestor)
	if len(below) < len(above) {
		return false
	}
	return path.Join(below[:len(above)]...) == path.Join(above...)
}

// Chmod sets the permission bits of name, leaving the directory bit as it is.
func (f *FS) Chmod(_ context.Context, name string, mode fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	found, err := f.walk(name)
	if err != nil {
		return fmt.Errorf("chmod %w", err)
	}

	found.mode = found.mode.Type() | mode.Perm()
	found.modTime = time.Now()
	return nil
}
