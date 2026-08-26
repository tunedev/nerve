package objectfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// Create stores an empty object at name, replacing whatever was there.
func (f *FS) Create(ctx context.Context, name string) error {
	return f.Write(ctx, name, nil)
}

// Mkdir has nothing to make. No directory exists apart from the entries under
// it, so the state the caller asked for is the state that already holds, and
// saying so is more useful than refusing a verb a caller cannot know to skip.
func (f *FS) Mkdir(_ context.Context, _ string) error { return nil }

// Read returns the whole content of name.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	stream, err := f.open(ctx, name)
	if err != nil {
		return nil, err
	}
	// The content is already in hand by the time the stream closes, so a close
	// error has nothing left to tell the caller about what it asked for.
	defer func() { _ = stream.Close() }()

	content, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	return content, nil
}

// Write replaces the content of name, creating it if it is not there.
//
// The mode survives the write, which costs a Head on every write and is what
// makes Chmod mean anything: a mode a later write silently reset would be a
// mode a caller cannot rely on having set.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	if isRoot(name) {
		return fmt.Errorf("write %q: the bucket itself: %w", name, fs.ErrInvalid)
	}

	mode, err := f.modeOf(ctx, name)
	if err != nil {
		return err
	}
	if err := f.bucket.Put(ctx, key(name), data, mode); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	return nil
}

// open is what Read and OpenRead share: the content of name as a stream.
func (f *FS) open(ctx context.Context, name string) (io.ReadCloser, error) {
	if isRoot(name) {
		return nil, fmt.Errorf("read %q: the bucket itself: %w", name, fs.ErrInvalid)
	}

	stream, _, err := f.bucket.Get(ctx, key(name))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	return stream, nil
}

// modeOf is the mode a write to name stores it under: the one it already
// carries, or the default when nothing is there yet.
func (f *FS) modeOf(ctx context.Context, name string) (fs.FileMode, error) {
	object, err := f.bucket.Head(ctx, key(name))
	switch {
	case err == nil:
		return object.Mode.Perm(), nil
	case errors.Is(err, fs.ErrNotExist):
		return fileMode, nil
	default:
		return 0, fmt.Errorf("write %q: %w", name, err)
	}
}
