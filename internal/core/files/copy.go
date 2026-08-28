package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Copy duplicates from onto to in s, a whole subtree if from is a directory.
//
// A destination already there is refused with fs.ErrExist rather than merged.
// Writing one tree over another leaves a directory holding both, and neither of
// them is what the caller asked to have a copy of.
//
// A destination beneath the source copies the source as it stood when the call
// began: the walk finishes before the first write, so the copy does not find
// itself.
func (t *Tree) Copy(ctx context.Context, s workspace.Scope, from, to string) error {
	inside, err := t.inside(s)
	if err != nil {
		return fmt.Errorf("copy %q to %q: %w", from, to, err)
	}
	if path.Join("/", from) == path.Join("/", to) {
		return fmt.Errorf("copy %q onto itself: %w", from, fs.ErrInvalid)
	}

	source, err := inside.Stat(ctx, from)
	if err != nil {
		return fmt.Errorf("copy %q to %q: %w", from, to, err)
	}
	if _, err := inside.Stat(ctx, to); err == nil {
		return fmt.Errorf("copy %q to %q: %w", from, to, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("copy %q to %q: %w", from, to, err)
	}

	if !source.IsDir() {
		if err := copyFile(ctx, inside, from, to); err != nil {
			return fmt.Errorf("copy %q to %q: %w", from, to, err)
		}
		return nil
	}

	entries, err := collect(ctx, inside, from, func(string, vfs.FileInfo) bool { return true })
	if err != nil {
		return fmt.Errorf("copy %q to %q: %w", from, to, err)
	}
	if err := inside.Mkdir(ctx, to); err != nil {
		return fmt.Errorf("copy %q to %q: %w", from, to, err)
	}
	for _, entry := range entries {
		target := path.Join(to, entry.Path)
		if entry.Info.IsDir() {
			if err := inside.Mkdir(ctx, target); err != nil {
				return fmt.Errorf("copy %q to %q: %w", from, to, err)
			}
			continue
		}
		if err := copyFile(ctx, inside, path.Join(from, entry.Path), target); err != nil {
			return fmt.Errorf("copy %q to %q: %w", from, to, err)
		}
	}
	return nil
}

// copyFile writes the content of from at to, making the directories on the way.
// An empty directory in the source is made rather than skipped, which is why
// the walk reports directories and this does not have to guess at them.
func copyFile(ctx context.Context, inside vfs.FileSystem, from, to string) error {
	data, err := inside.Read(ctx, from)
	if err != nil {
		return err
	}
	if err := inside.Mkdir(ctx, path.Dir(to)); err != nil {
		return err
	}
	return inside.Write(ctx, to, data)
}
