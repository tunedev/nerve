package streamrotatefs

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
)

// Remove deletes the entry at name. A prefix is not an entry, so removing one
// is removing nothing: what is under it is RemoveAll's business.
//
// It takes the one name it was given. A log's sealed segments are entries of
// their own, so removing the name a log is written to leaves the segments it
// rotated out; RemoveAll of the prefix above them is what takes those.
func (f *FS) Remove(_ context.Context, name string) error {
	at, err := keyspace.Key(name)
	if err != nil {
		return fmt.Errorf("remove %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, held := f.logs[at]; !held {
		return fmt.Errorf("remove %q: %w", name, fs.ErrNotExist)
	}
	delete(f.logs, at)
	return nil
}

// RemoveAll deletes the entry at name and every entry under it. A path that is
// not there is not an error, and the root is a prefix rather than an entry, so
// RemoveAll of it empties the mount rather than failing on it.
func (f *FS) RemoveAll(_ context.Context, name string) error {
	under := keyspace.Prefix(name)

	f.mu.Lock()
	defer f.mu.Unlock()

	if at, err := keyspace.Key(name); err == nil {
		delete(f.logs, at)
	}
	maps.DeleteFunc(f.logs, func(at string, _ *stream) bool { return strings.HasPrefix(at, under) })
	return nil
}
