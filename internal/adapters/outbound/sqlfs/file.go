package sqlfs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Create makes name exist as an empty entry, truncating it if it already does.
func (f *FS) Create(ctx context.Context, name string) error {
	if err := f.store(ctx, name, nil); err != nil {
		return fmt.Errorf("create %w", err)
	}
	return nil
}

// Write replaces the content of name, creating it if it does not exist. The
// driver copies what it is given on the way to the database, so nothing the
// caller does to the slice afterwards reaches what is stored.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	if err := f.store(ctx, name, data); err != nil {
		return fmt.Errorf("write %w", err)
	}
	return nil
}

// store is what Create and Write both are: the whole content of one entry,
// replacing what was there. They differ only in what they store.
func (f *FS) store(ctx context.Context, name string, data []byte) error {
	return f.inTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := holder(ctx, tx, name); err != nil {
			return err
		}

		switch existing, err := lookup(ctx, tx, name); {
		case errors.Is(err, fs.ErrNotExist):
			return insert(ctx, tx, name, fileMode, data)
		case err != nil:
			return err
		case existing.isDir():
			return fmt.Errorf("%q: a directory: %w", name, fs.ErrInvalid)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE entries SET data = ?, mod_time = ? WHERE path = ?`,
			blob(data), time.Now().UnixNano(), clean(name)); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		return nil
	})
}

// Mkdir makes name exist as a directory, creating missing parents. A directory
// that is already there is not an error; a file in its place is.
func (f *FS) Mkdir(ctx context.Context, name string) error {
	err := f.inTx(ctx, func(tx *sql.Tx) error {
		for _, ancestor := range chain(name) {
			switch existing, err := lookup(ctx, tx, ancestor); {
			case errors.Is(err, fs.ErrNotExist):
				if err := insert(ctx, tx, ancestor, dirMode, nil); err != nil {
					return err
				}
			case err != nil:
				return err
			case !existing.isDir():
				return fmt.Errorf("%q: a file is in the way at %q: %w", name, ancestor, fs.ErrExist)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("mkdir %w", err)
	}
	return nil
}

// chain lists name and every directory above it, nearest the root first, which
// is the order they have to be made in.
func chain(name string) []string {
	cleaned := clean(name)
	if cleaned == "/" {
		return nil
	}

	var (
		ancestors []string
		walked    string
	)
	for _, element := range strings.Split(cleaned[1:], "/") {
		walked += "/" + element
		ancestors = append(ancestors, walked)
	}
	return ancestors
}

// Read returns the whole content of name.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	var (
		data []byte
		mode int64
	)
	row := f.db.QueryRowContext(ctx, `SELECT mode, data FROM entries WHERE path = ?`, clean(name))
	switch err := row.Scan(&mode, &data); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("read %q: %w", name, fs.ErrNotExist)
	case err != nil:
		return nil, fmt.Errorf("read %q: %w", name, err)
	}

	if fs.FileMode(mode).IsDir() { //nolint:gosec // the column holds what fs.FileMode was written as
		return nil, fmt.Errorf("read %q: a directory: %w", name, fs.ErrInvalid)
	}
	return bytes.Clone(data), nil
}
