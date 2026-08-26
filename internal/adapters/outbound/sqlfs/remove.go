package sqlfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
)

// Remove deletes name. It fails on a directory that is not empty, because
// deleting what is inside one is RemoveAll's business and doing it here would
// make a typo destructive.
func (f *FS) Remove(ctx context.Context, name string) error {
	err := f.inTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := holder(ctx, tx, name); err != nil {
			return err
		}
		found, err := lookup(ctx, tx, name)
		if err != nil {
			return err
		}

		if found.isDir() {
			held, err := children(ctx, tx, name)
			if err != nil {
				return err
			}
			if held > 0 {
				return fmt.Errorf("%q: %d entries are still in it: %w", name, held, fs.ErrExist)
			}
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE path = ?`, clean(name)); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remove %w", err)
	}
	return nil
}

// RemoveAll deletes name and everything beneath it. A path that is not there is
// not an error. The root is the one path that cannot be deleted, so it is
// emptied instead: everything beneath it is what the verb promises either way.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if isRoot(name) {
		if _, err := f.db.ExecContext(ctx, `DELETE FROM entries WHERE path <> '/'`); err != nil {
			return fmt.Errorf("remove_all %q: %w", name, transient(err))
		}
		return nil
	}

	err := f.inTx(ctx, func(tx *sql.Tx) error {
		switch _, _, err := holder(ctx, tx, name); {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		}

		below := prefixOf(name)
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM entries WHERE path = ? OR `+beneath,
			clean(name), below, below); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remove_all %w", err)
	}
	return nil
}

// beneath matches every row under a path, given that path with its separator as
// both parameters. It compares a prefix rather than using LIKE, because a path
// is an operator's string and LIKE would read a literal "%" in one as a
// wildcard. SQLite measures the prefix so the count is in the same units as
// substr, which counts characters where Go would count bytes.
const beneath = `substr(path, 1, length(?)) = ?`

// prefixOf is what every path beneath name starts with.
func prefixOf(name string) string {
	cleaned := clean(name)
	if cleaned == "/" {
		return cleaned
	}
	return cleaned + "/"
}

// children counts what is directly inside a directory, which is the difference
// between Remove and RemoveAll.
func children(ctx context.Context, tx *sql.Tx, name string) (int, error) {
	var held int
	row := tx.QueryRowContext(ctx, `SELECT count(*) FROM entries WHERE parent = ?`, clean(name))
	if err := row.Scan(&held); err != nil {
		return 0, fmt.Errorf("%q: %w", name, err)
	}
	return held, nil
}
