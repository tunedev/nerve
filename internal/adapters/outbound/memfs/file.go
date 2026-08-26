package memfs

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"time"
)

// Create makes name exist as an empty file, truncating it if it already does.
func (f *FS) Create(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	holder, base, err := f.parent(name)
	if err != nil {
		return fmt.Errorf("create %w", err)
	}
	if existing, taken := holder.children[base]; taken {
		if existing.isDir() {
			return fmt.Errorf("create %q: a directory: %w", name, fs.ErrInvalid)
		}
		existing.data = nil
		existing.modTime = time.Now()
		return nil
	}

	holder.children[base] = &node{name: base, mode: fileMode, modTime: time.Now()}
	return nil
}

// Mkdir makes name exist as a directory, creating missing parents. A directory
// that is already there is not an error; a file in its place is.
func (f *FS) Mkdir(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	current := f.root
	for _, element := range elements(name) {
		child, exists := current.children[element]
		if !exists {
			child = newDir(element)
			current.children[element] = child
		}
		if !child.isDir() {
			return fmt.Errorf("mkdir %q: a file is in the way: %w", name, fs.ErrExist)
		}
		current = child
	}
	return nil
}

// Read returns the whole content of name.
func (f *FS) Read(_ context.Context, name string) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	found, err := f.walk(name)
	if err != nil {
		return nil, fmt.Errorf("read %w", err)
	}
	if found.isDir() {
		return nil, fmt.Errorf("read %q: a directory: %w", name, fs.ErrInvalid)
	}
	return bytes.Clone(found.data), nil
}

// Write replaces the content of name, creating it if it does not exist.
func (f *FS) Write(_ context.Context, name string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	holder, base, err := f.parent(name)
	if err != nil {
		return fmt.Errorf("write %w", err)
	}

	existing, taken := holder.children[base]
	if !taken {
		holder.children[base] = &node{name: base, mode: fileMode, modTime: time.Now(), data: bytes.Clone(data)}
		return nil
	}
	if existing.isDir() {
		return fmt.Errorf("write %q: a directory: %w", name, fs.ErrInvalid)
	}
	existing.data = bytes.Clone(data)
	existing.modTime = time.Now()
	return nil
}
