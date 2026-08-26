package sqlfs

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsTruncater reports that an entry can be cut to a length. sqlfs answers this
// accessor and refuses the other two: a link would need semantics the port does
// not decide and no storage kind here has yet, and a stream would be this
// package reading the whole row into a buffer and calling it a piece at a time.
func (f *FS) AsTruncater() vfs.Truncater { return f }

// Truncate sets the length of name's content, padding with zero bytes when size
// is past the end.
//
// The resize happens here rather than in SQL because SQLite's concatenation
// operator produces text: appending zero bytes to a blob with it would hand
// back a string, and what is stored is bytes.
func (f *FS) Truncate(ctx context.Context, name string, size int64) error {
	if size < 0 {
		return fmt.Errorf("truncate %q to %d: a length is not negative: %w", name, size, fs.ErrInvalid)
	}

	err := f.inTx(ctx, func(tx *sql.Tx) error {
		found, err := lookup(ctx, tx, name)
		if err != nil {
			return err
		}
		if found.isDir() {
			return fmt.Errorf("%q: a directory: %w", name, fs.ErrInvalid)
		}

		var data []byte
		row := tx.QueryRowContext(ctx, `SELECT data FROM entries WHERE path = ?`, clean(name))
		if err := row.Scan(&data); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}

		cut := make([]byte, size)
		copy(cut, data)
		if _, err := tx.ExecContext(ctx,
			`UPDATE entries SET data = ?, mod_time = ? WHERE path = ?`,
			cut, time.Now().UnixNano(), clean(name)); err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("truncate %w", err)
	}
	return nil
}
