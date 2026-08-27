// Package treecopy is the working copy over a mounted tree: the content is the
// tree under one root, and where the copy stands is one file beside it.
//
// Beside it, not in it. A copy's content is only what a caller put there, so a
// file recording where it stands would be a file every version records and
// every diff reports. Keeping it out of the root is what lets the same mount
// hold the copy, the objects and the refs without any of them being part of
// another.
package treecopy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Copy is one working copy: the tree beneath root of a mount, standing at the
// version recorded in state.
type Copy struct {
	fsys  vfs.FileSystem
	root  string
	state string
}

// New returns the copy rooted at root of fsys, recording where it stands in
// state. It touches nothing: an empty root is an empty copy and a state file
// that is not there yet is a copy standing nowhere, so a copy that has never
// been used costs no I/O to construct.
func New(fsys vfs.FileSystem, root, state string) *Copy {
	return &Copy{fsys: fsys, root: path.Join("/", root), state: path.Join("/", state)}
}

// Walk visits every file beneath the root, with its path relative to it.
func (c *Copy) Walk(ctx context.Context, visit func(path string, data []byte) error) error {
	return c.walk(ctx, "", visit)
}

// Write replaces the content at name, making the directories on the way.
func (c *Copy) Write(ctx context.Context, name string, data []byte) error {
	full := path.Join(c.root, name)
	if err := c.fsys.Mkdir(ctx, path.Dir(full)); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	if err := c.fsys.Write(ctx, full, data); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	return nil
}

// Remove deletes name. Directories the removal empties stay: the copy records
// content at paths and nothing about the directories on the way.
func (c *Copy) Remove(ctx context.Context, name string) error {
	if err := c.fsys.Remove(ctx, path.Join(c.root, name)); err != nil {
		return fmt.Errorf("remove %q: %w", name, err)
	}
	return nil
}

// Base returns the version the copy was taken from. A copy that has never been
// put anywhere stands nowhere, which is the zero ID rather than an error.
func (c *Copy) Base(ctx context.Context) (version.ID, error) {
	data, err := c.fsys.Read(ctx, c.state)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read where %q stands: %w", c.root, err)
	}
	return version.ID(data), nil
}

// SetBase records that the copy now stands at id.
func (c *Copy) SetBase(ctx context.Context, id version.ID) error {
	if err := c.fsys.Mkdir(ctx, path.Dir(c.state)); err != nil {
		return fmt.Errorf("record where %q stands: %w", c.root, err)
	}
	if err := c.fsys.Write(ctx, c.state, []byte(id)); err != nil {
		return fmt.Errorf("record where %q stands: %w", c.root, err)
	}
	return nil
}

// walk descends one directory of the copy. A root that is not there yet holds
// no files rather than failing, which is what an empty copy is.
func (c *Copy) walk(ctx context.Context, prefix string, visit func(path string, data []byte) error) error {
	entries, err := c.fsys.ReadDir(ctx, path.Join(c.root, prefix))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("walk %q: %w", prefix, err)
	}

	for _, found := range entries {
		name := path.Join(prefix, found.Name)
		if found.IsDir() {
			if err := c.walk(ctx, name, visit); err != nil {
				return err
			}
			continue
		}
		data, err := c.fsys.Read(ctx, path.Join(c.root, name))
		if err != nil {
			return fmt.Errorf("read %q: %w", name, err)
		}
		if err := visit(name, data); err != nil {
			return err
		}
	}
	return nil
}
