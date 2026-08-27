package streamfs

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"strings"
)

// Remove deletes the stream at name. A prefix is not a stream, so removing one
// is removing nothing: what is under it is RemoveAll's business.
func (f *FS) Remove(_ context.Context, name string) error {
	at, err := key(name)
	if err != nil {
		return fmt.Errorf("remove %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, held := f.streams[at]; !held {
		return fmt.Errorf("remove %q: %w", name, fs.ErrNotExist)
	}
	delete(f.streams, at)
	return nil
}

// RemoveAll deletes the stream at name and every stream under it. A path that
// is not there is not an error, and the root is a prefix rather than a stream,
// so RemoveAll of it empties the mount rather than failing on it.
//
// A reader following a stream that is removed keeps the bytes it had not read
// yet: it holds the ring, and what it hands back is what was written before the
// stream stopped being reachable by name.
func (f *FS) RemoveAll(_ context.Context, name string) error {
	under := prefix(name)

	f.mu.Lock()
	defer f.mu.Unlock()

	if at, err := key(name); err == nil {
		delete(f.streams, at)
	}
	maps.DeleteFunc(f.streams, func(at string, _ *ring) bool { return strings.HasPrefix(at, under) })
	return nil
}
