package localfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// Create makes name exist as an empty file, truncating it if it already does.
func (f *FS) Create(_ context.Context, name string) error {
	file, err := os.OpenFile(f.host(name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("create %q: %w", name, invalid(err))
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	return nil
}

// Mkdir makes name exist as a directory, creating missing parents. A directory
// that is already there is not an error; a file in its place is.
func (f *FS) Mkdir(_ context.Context, name string) error {
	err := os.MkdirAll(f.host(name), dirPerm)
	if errors.Is(err, syscall.ENOTDIR) {
		return fmt.Errorf("mkdir %q: a file is in the way: %w", name, fs.ErrExist)
	}
	if err != nil {
		return fmt.Errorf("mkdir %q: %w", name, err)
	}
	return nil
}

// Read returns the whole content of name.
func (f *FS) Read(_ context.Context, name string) ([]byte, error) {
	content, err := os.ReadFile(f.host(name))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, invalid(err))
	}
	return content, nil
}

// Write replaces the content of name, creating it if it does not exist. An
// entry already there keeps the mode it was given, because Chmod is the only
// verb that sets one.
func (f *FS) Write(_ context.Context, name string, data []byte) error {
	if err := os.WriteFile(f.host(name), data, filePerm); err != nil {
		return fmt.Errorf("write %q: %w", name, invalid(err))
	}
	return nil
}
