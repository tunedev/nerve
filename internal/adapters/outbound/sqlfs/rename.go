package sqlfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Rename moves oldPath to newPath, replacing newPath if it exists. It refuses
// to replace a directory with entries in it, for the reason Remove refuses to
// delete one.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	if clean(oldPath) == clean(newPath) {
		return nil
	}
	if descends(newPath, oldPath) {
		return fmt.Errorf("rename %q to %q: a directory cannot hold itself: %w", oldPath, newPath, fs.ErrInvalid)
	}

	err := f.inTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := holder(ctx, tx, oldPath); err != nil {
			return err
		}
		if _, err := lookup(ctx, tx, oldPath); err != nil {
			return err
		}
		if _, _, err := holder(ctx, tx, newPath); err != nil {
			return err
		}

		if err := clearLanding(ctx, tx, oldPath, newPath); err != nil {
			return err
		}
		return move(ctx, tx, oldPath, newPath)
	})
	if err != nil {
		return fmt.Errorf("rename %w", err)
	}
	return nil
}

// clearLanding makes room for the move. What is already at newPath is replaced,
// unless it is a directory with entries in it.
func clearLanding(ctx context.Context, tx *sql.Tx, oldPath, newPath string) error {
	switch landing, err := lookup(ctx, tx, newPath); {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case landing.isDir():
		held, err := children(ctx, tx, newPath)
		if err != nil {
			return err
		}
		if held > 0 {
			return fmt.Errorf("%q to %q: %d entries are still in it: %w", oldPath, newPath, held, fs.ErrExist)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE path = ?`, clean(newPath)); err != nil {
		return fmt.Errorf("%q: %w", newPath, err)
	}
	return nil
}

// move rewrites the entry's own row and then the paths of everything under it.
// A path is stored whole, so a subtree moves by rewriting its prefix: the two
// statements are what a tree of parent pointers would do in one, and what makes
// every read a lookup rather than a walk.
func move(ctx context.Context, tx *sql.Tx, oldPath, newPath string) error {
	parent, base := split(newPath)
	if _, err := tx.ExecContext(ctx,
		`UPDATE entries SET path = ?, parent = ?, name = ?, mod_time = ? WHERE path = ?`,
		clean(newPath), parent, base, time.Now().UnixNano(), clean(oldPath)); err != nil {
		return fmt.Errorf("%q to %q: %w", oldPath, newPath, err)
	}

	from, to := prefixOf(oldPath), prefixOf(newPath)
	if _, err := tx.ExecContext(ctx,
		`UPDATE entries
		 SET path   = ? || substr(path, length(?) + 1),
		     parent = ? || substr(parent, length(?) + 1)
		 WHERE `+beneath,
		to, from, clean(newPath), clean(oldPath), from, from); err != nil {
		return fmt.Errorf("%q to %q: %w", oldPath, newPath, err)
	}
	return nil
}

// descends reports whether name is at or beneath ancestor, which is what a move
// into a directory's own subtree looks like.
func descends(name, ancestor string) bool {
	below, above := clean(name), clean(ancestor)
	return below == above || strings.HasPrefix(below, prefixOf(above))
}

// Chmod sets the permission bits of name, leaving the directory bit as it is.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	err := f.inTx(ctx, func(tx *sql.Tx) error {
		found, err := lookup(ctx, tx, name)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE entries SET mode = ?, mod_time = ? WHERE path = ?`,
			int64(found.mode.Type()|mode.Perm()), time.Now().UnixNano(), clean(name)); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("chmod %w", err)
	}
	return nil
}
