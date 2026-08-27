package streamrotatefs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the entry at oldPath to newPath, replacing what is there. It
// moves one entry: a prefix is not an entry, and the entries under one are not
// a subtree that moves with it.
//
// A log takes its history with it as a record and not as entries. It goes on
// dropping the segments it sealed under the name it had, because those are the
// names it sealed them under, and it seals what it rotates from here on under
// the name it now has.
func (f *FS) Rename(_ context.Context, oldPath, newPath string) error {
	from, err := key(oldPath)
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	to, err := key(newPath)
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	if from == to {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	held, there := f.logs[from]
	if !there {
		return fmt.Errorf("rename %q: %w", oldPath, fs.ErrNotExist)
	}
	delete(f.logs, from)
	f.logs[to] = held
	return nil
}

// Chmod sets the permission bits of the entry at name. There is no directory
// bit to leave alone: a log is never a directory.
func (f *FS) Chmod(_ context.Context, name string, mode fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	held, err := f.find(name)
	if err != nil {
		return fmt.Errorf("chmod %w", err)
	}
	held.mode = mode.Perm()
	return nil
}
