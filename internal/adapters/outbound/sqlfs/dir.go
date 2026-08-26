package sqlfs

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without reading it.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	found, err := lookup(ctx, f.db, name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %w", transient(err))
	}
	return found.info(), nil
}

// ReadDir lists the direct children of name, sorted by name. The sort is the
// database's: a path is stored whole, so the children of one are an index
// lookup on the parent column rather than a walk.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	found, err := lookup(ctx, f.db, name)
	if err != nil {
		return nil, fmt.Errorf("read_dir %w", transient(err))
	}
	if !found.isDir() {
		return nil, fmt.Errorf("read_dir %q: not a directory: %w", name, fs.ErrInvalid)
	}

	rows, err := f.db.QueryContext(ctx,
		`SELECT name, mode, mod_time, length(data) FROM entries WHERE parent = ? ORDER BY name`,
		clean(name))
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, transient(err))
	}
	defer func() { _ = rows.Close() }()

	var entries []vfs.FileInfo
	for rows.Next() {
		var (
			child entry
			mode  int64
		)
		if err := rows.Scan(&child.name, &mode, &child.modTime, &child.size); err != nil {
			return nil, fmt.Errorf("read_dir %q: %w", name, transient(err))
		}
		child.mode = fs.FileMode(mode) //nolint:gosec // the column holds what fs.FileMode was written as
		entries = append(entries, child.info())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, transient(err))
	}
	return entries, nil
}
