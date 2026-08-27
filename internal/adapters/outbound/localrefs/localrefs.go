// Package localrefs keeps refs as one file per ref under a directory of a
// mounted tree. It is the ref store for a single nerved: memfs under test,
// localfs on the laptop.
//
// Local is about who agrees on the outcome of a race, not about where the bytes
// land. A compare-and-set here is a read and a write under one mutex, so it is
// atomic for this process and for no other; the Postgres ref store is where a
// second writer gets a truthful answer.
package localrefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Store keeps the refs of one tree under one directory.
type Store struct {
	mu   sync.Mutex
	fsys vfs.FileSystem
	root string
}

// New returns a store keeping its refs under root of fsys. It touches nothing:
// the directory is made by the first write, so a store that has never been
// written to costs no I/O to construct.
func New(fsys vfs.FileSystem, root string) *Store {
	return &Store{fsys: fsys, root: root}
}

// Resolve returns the version name stands for.
func (s *Store) Resolve(ctx context.Context, name string) (version.ID, error) {
	if err := version.ValidateRefName(name); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(ctx, name)
}

// List returns every name that stands for a version, sorted.
func (s *Store) List(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.fsys.ReadDir(ctx, s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list refs under %q: %w", s.root, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names, nil
}

// Set points name at next, unless it has moved off expected since the caller
// read it.
func (s *Store) Set(ctx context.Context, name string, expected, next version.ID) error {
	if err := version.ValidateRefName(name); err != nil {
		return err
	}
	if next == "" {
		return fmt.Errorf("ref %q cannot be set to no version: %w", name, fs.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.expect(ctx, name, expected); err != nil {
		return err
	}
	if err := s.fsys.Mkdir(ctx, s.root); err != nil {
		return fmt.Errorf("make ref directory %q: %w", s.root, err)
	}
	if err := s.fsys.Write(ctx, s.pathOf(name), []byte(next)); err != nil {
		return fmt.Errorf("set ref %q: %w", name, err)
	}
	return nil
}

// Delete unsets name, unless it has moved off expected since the caller read it.
func (s *Store) Delete(ctx context.Context, name string, expected version.ID) error {
	if err := version.ValidateRefName(name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.expect(ctx, name, expected); err != nil {
		return err
	}
	if err := s.fsys.Remove(ctx, s.pathOf(name)); err != nil {
		return fmt.Errorf("delete ref %q: %w", name, err)
	}
	return nil
}

// expect fails with version.ErrConflict unless name stands for want. An unset
// ref stands for the zero ID, which is how creating one is expressed; the
// caller that expects the zero ID of a ref it is deleting gets fs.ErrNotExist
// from the removal instead.
func (s *Store) expect(ctx context.Context, name string, want version.ID) error {
	current, err := s.read(ctx, name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if current != want {
		return fmt.Errorf("ref %q stands for %q, not %q: %w", name, current, want, version.ErrConflict)
	}
	return nil
}

// read returns what name stands for, and fs.ErrNotExist with the zero ID when
// it stands for nothing.
func (s *Store) read(ctx context.Context, name string) (version.ID, error) {
	data, err := s.fsys.Read(ctx, s.pathOf(name))
	if err != nil {
		return "", fmt.Errorf("read ref %q: %w", name, err)
	}
	return version.ID(data), nil
}

func (s *Store) pathOf(name string) string { return path.Join(s.root, name) }
