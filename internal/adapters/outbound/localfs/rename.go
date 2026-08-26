package localfs

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
)

// Rename moves oldPath to newPath, replacing newPath if it exists. It refuses
// to replace a directory with entries in it, for the reason Remove refuses to
// delete one.
func (f *FS) Rename(_ context.Context, oldPath, newPath string) error {
	if path.Join("/", oldPath) == path.Join("/", newPath) {
		return nil
	}
	if isRoot(oldPath) {
		return fmt.Errorf("rename %q: the root: %w", oldPath, fs.ErrInvalid)
	}
	if descends(newPath, oldPath) {
		return fmt.Errorf("rename %q to %q: a directory cannot hold itself: %w", oldPath, newPath, fs.ErrInvalid)
	}
	if err := f.clearLanding(oldPath, newPath); err != nil {
		return err
	}

	if err := os.Rename(f.host(oldPath), f.host(newPath)); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, invalid(err))
	}
	return nil
}

// clearLanding refuses a landing that is a directory with entries in it, and
// takes an empty one out of the way when the entry moving onto it is a file.
// The host refuses that pairing, and the port promises a rename replaces what
// it lands on whatever the two are.
func (f *FS) clearLanding(oldPath, newPath string) error {
	landing, err := os.Lstat(f.host(newPath))
	if err != nil {
		return nil
	}

	if landing.IsDir() {
		children, err := os.ReadDir(f.host(newPath))
		if err != nil {
			return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, invalid(err))
		}
		if len(children) > 0 {
			return fmt.Errorf("rename %q to %q: %d entries are still in it: %w", oldPath, newPath, len(children), fs.ErrExist)
		}
	}

	moving, err := os.Lstat(f.host(oldPath))
	if err != nil || moving.IsDir() == landing.IsDir() {
		return nil
	}
	if err := os.Remove(f.host(newPath)); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, invalid(err))
	}
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

// Chmod sets the permission bits of name. The host keeps the kind bits, which
// is what leaves the directory bit alone.
func (f *FS) Chmod(_ context.Context, name string, mode fs.FileMode) error {
	if err := os.Chmod(f.host(name), mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", name, invalid(err))
	}
	return nil
}
