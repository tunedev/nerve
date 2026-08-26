// Package sqlfs keeps a tree in a SQLite database. It is the first plugin whose
// storage outlives the process: the same file mounted again holds what was
// written before.
//
// One row is one entry, keyed by its whole path rather than by a parent
// pointer. A tree of rows would make ReadDir a join and Rename a single update;
// keying by path makes ReadDir an index lookup on the parent column and Rename
// a prefix rewrite. Every verb here is a path lookup, so the shape that makes
// lookups free is the shape to keep.
//
// The driver is pure Go, because the build sets CGO_ENABLED=0 to keep the
// binary reproducible and a cgo SQLite would put the host C toolchain inside
// it.
package sqlfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// dirMode and fileMode are what an entry gets when it is made. Mode is not a
// parameter of creation anywhere in the port, so Chmod is the only way to
// change either.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// busyTimeout is how long SQLite waits for a lock before giving up. One
// connection leaves nothing inside this process to contend with; a second
// process on the same file is what this waits out.
const busyTimeout = 5 * time.Second

// FS is one tree in one SQLite database.
//
// The pool is held to a single connection. SQLite serialises writers whatever
// the pool does, so more connections buy no write concurrency: they only move
// the queue from Go's pool into SQLite's lock, where waiting is a timeout to
// get wrong and running out of it is SQLITE_BUSY. One connection is the same
// queue with nothing to tune.
type FS struct {
	vfs.NoAccessors

	db *sql.DB
}

// schema is what an empty database is. STRICT because a column that accepts
// anything is a column that eventually holds a string where a mode should be.
const schema = `
CREATE TABLE IF NOT EXISTS entries (
	path     TEXT PRIMARY KEY,
	parent   TEXT NOT NULL,
	name     TEXT NOT NULL,
	mode     INTEGER NOT NULL,
	mod_time INTEGER NOT NULL,
	data     BLOB NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS entries_parent ON entries(parent);
`

// rootRow is the ground every path resolves against. No verb makes it, so
// opening does, and a database opened again already has it.
const rootRow = `
INSERT OR IGNORE INTO entries(path, parent, name, mode, mod_time, data)
VALUES ('/', '', '/', ?, ?, x'')
`

// Open returns the tree held in the database at file, creating the file and its
// schema when they are not there yet.
func Open(file string) (*FS, error) {
	db, err := sql.Open("sqlite", dsn(file))
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", file, err)
	}
	db.SetMaxOpenConns(1)

	for _, step := range []func() error{
		func() error { _, err := db.Exec(schema); return err },
		func() error { _, err := db.Exec(rootRow, int64(dirMode), time.Now().UnixNano()); return err },
	} {
		if err := step(); err != nil {
			return nil, errors.Join(fmt.Errorf("open %q: %w", file, err), db.Close())
		}
	}
	return &FS{db: db}, nil
}

// dsn spells the file as the driver wants it, with the pragmas that make a
// database on a laptop behave: a write-ahead log so a reader is not blocked by
// a writer, and a wait rather than an immediate failure when another process
// holds the lock.
//
// Transactions take their write lock at BEGIN. Taking it later means two
// transactions can both read and then both try to upgrade, which SQLite can
// only resolve by failing one of them.
func dsn(file string) string {
	pragmas := url.Values{"_pragma": {
		"journal_mode(WAL)",
		fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()),
	}}
	pragmas.Set("_txlock", "immediate")
	return "file:" + file + "?" + pragmas.Encode()
}

// Close releases the database. The port has no lifecycle verb, so a mount built
// from config is closed by the process ending; a test that opens a tree closes
// it itself.
func (f *FS) Close() error {
	if err := f.db.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// Capabilities reports a plain read-write tree, which is what sqlfs is at every
// path.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{} }

// clean resolves a path lexically, so a caller is not made to clean paths a
// mount table would have cleaned. Everything below works in cleaned paths and
// the root is "/".
func clean(name string) string {
	return path.Clean("/" + strings.TrimPrefix(name, "/"))
}

// isRoot reports whether name is the ground the tree stands on, which no verb
// makes, moves or deletes.
func isRoot(name string) bool { return clean(name) == "/" }

// split names the entry and the directory holding it, which is what a verb that
// makes, moves or removes an entry needs.
func split(name string) (parent, base string) {
	cleaned := clean(name)
	return path.Dir(cleaned), path.Base(cleaned)
}

// entry is one row as the verbs read it. Size comes from the database rather
// than from the content, so a Stat does not carry a blob back.
type entry struct {
	name    string
	mode    fs.FileMode
	modTime int64
	size    int64
}

func (e entry) isDir() bool { return e.mode.IsDir() }

func (e entry) info() vfs.FileInfo {
	size := e.size
	if e.isDir() {
		size = 0
	}
	return vfs.FileInfo{Name: e.name, Size: size, Mode: e.mode, ModTime: time.Unix(0, e.modTime)}
}

// lookup reads the row at name. A path that is not there is fs.ErrNotExist,
// which is every verb's answer to it.
func lookup(ctx context.Context, q querier, name string) (entry, error) {
	var (
		found entry
		mode  int64
	)
	row := q.QueryRowContext(ctx, `SELECT name, mode, mod_time, length(data) FROM entries WHERE path = ?`, clean(name))
	switch err := row.Scan(&found.name, &mode, &found.modTime, &found.size); {
	case errors.Is(err, sql.ErrNoRows):
		return entry{}, fmt.Errorf("%q: %w", name, fs.ErrNotExist)
	case err != nil:
		return entry{}, fmt.Errorf("%q: %w", name, err)
	}
	found.mode = fs.FileMode(mode) //nolint:gosec // the column holds what fs.FileMode was written as
	return found, nil
}

// holder reads the directory an entry goes in. The root is in nothing, so it
// has no holder, and a file in a directory's place is the same refusal a walk
// down a tree would give.
func holder(ctx context.Context, q querier, name string) (parent, base string, err error) {
	if isRoot(name) {
		return "", "", fmt.Errorf("%q: the root: %w", name, fs.ErrInvalid)
	}

	parent, base = split(name)
	found, err := lookup(ctx, q, parent)
	if err != nil {
		return "", "", err
	}
	if !found.isDir() {
		return "", "", fmt.Errorf("%q: %q is not a directory: %w", name, parent, fs.ErrInvalid)
	}
	return parent, base, nil
}

// querier is what a verb needs of the database, so the same helper reads inside
// a transaction and outside one.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// inTx runs work in one transaction, committing when it returns nil. A verb
// that reads before it writes needs the two to be one step, or a second caller
// lands between them.
func (f *FS) inTx(ctx context.Context, work func(*sql.Tx) error) error {
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := work(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// blob is what the data column takes. The column is NOT NULL because an entry
// always has content, empty when nothing was written into it; a nil would be a
// third state meaning what empty already means.
func blob(data []byte) []byte {
	if data == nil {
		return []byte{}
	}
	return data
}

// insert puts one entry in the directory that holds it.
func insert(ctx context.Context, tx *sql.Tx, name string, mode fs.FileMode, data []byte) error {
	parent, base := split(name)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO entries(path, parent, name, mode, mod_time, data) VALUES (?, ?, ?, ?, ?, ?)`,
		clean(name), parent, base, int64(mode), time.Now().UnixNano(), blob(data))
	if err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	return nil
}
