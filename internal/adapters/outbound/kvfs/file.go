package kvfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

// Create makes name exist as an empty value, truncating it if it already does.
func (f *FS) Create(ctx context.Context, name string) error {
	if err := f.replace(ctx, name, nil); err != nil {
		return fmt.Errorf("create %w", err)
	}
	return nil
}

// Mkdir has nothing to make. A name is a prefix, and a prefix exists only
// because keys are under it, so there is nothing to put in the keyspace and
// nothing for a caller to have done wrong.
func (f *FS) Mkdir(context.Context, string) error { return nil }

// Read returns the whole content of name.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	key, err := f.key(name)
	if err != nil {
		return nil, fmt.Errorf("read %w", err)
	}

	value, err := f.store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	return value.Data, nil
}

// Write replaces the content of name, creating it if it does not exist.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	if err := f.replace(ctx, name, data); err != nil {
		return fmt.Errorf("write %w", err)
	}
	return nil
}

// replace stores data under name, keeping the mode of a value already there:
// Chmod is the one verb that sets a mode, so writing content is not where one
// changes.
func (f *FS) replace(ctx context.Context, name string, data []byte) error {
	key, err := f.key(name)
	if err != nil {
		return err
	}

	mode, err := f.modeOf(ctx, key)
	if err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	if err := f.store.Put(ctx, key, Value{Data: data, Mode: mode, ModTime: time.Now()}); err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	return nil
}

// modeOf is the mode a value keeps through a write, which is the default when
// nothing is stored under key yet. A store that fails for any other reason is
// not a store with nothing in it, and says so here rather than writing a value
// under a mode it invented.
func (f *FS) modeOf(ctx context.Context, key string) (fs.FileMode, error) {
	existing, err := f.store.Get(ctx, key)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileMode, nil
	case err != nil:
		return 0, err
	default:
		return existing.Mode, nil
	}
}
