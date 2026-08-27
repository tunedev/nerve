package streamfs

import (
	"context"
	"fmt"
	"io/fs"
)

// Rename moves the stream at oldPath to newPath, replacing what is there. It
// moves one stream: a prefix is not a stream, and the streams under one are not
// a subtree that moves with it.
//
// The stream keeps its bytes and its readers. A follower holds the ring rather
// than the name, so a rename is not something it notices.
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

	stream, held := f.streams[from]
	if !held {
		return fmt.Errorf("rename %q: %w", oldPath, fs.ErrNotExist)
	}
	delete(f.streams, from)
	f.streams[to] = stream
	return nil
}

// Chmod sets the permission bits of the stream at name. There is no directory
// bit to leave alone: a stream is never a directory.
func (f *FS) Chmod(_ context.Context, name string, mode fs.FileMode) error {
	stream, err := f.find(name)
	if err != nil {
		return fmt.Errorf("chmod %w", err)
	}
	stream.chmod(mode)
	return nil
}
