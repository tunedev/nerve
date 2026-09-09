package sqlfs

import (
	"errors"
	"fmt"

	"github.com/tunedev/nerve/internal/core/stability"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// primaryCode is the mask that takes the primary result code out of an extended
// one. SQLite reports "busy because of a snapshot" as 517 and "busy, timed out"
// as 773; both are SQLITE_BUSY with a reason in the high bytes.
const primaryCode = 0xff

// held reports whether err is SQLite saying somebody else has the file. It is
// the one driver error here that another attempt might not see: a lock is a
// wait that ran out, not an answer about the tree.
func held(err error) bool {
	var coded *sqlite.Error
	if !errors.As(err, &coded) {
		return false
	}

	switch coded.Code() & primaryCode {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

// transient marks a lock SQLite could not get, so the retry layer sees the one
// failure it exists for. Everything else passes through unmarked and is
// classified fail-fast, which is the right default for an error nobody has
// sorted.
//
// busy_timeout already absorbs a wait this process can win. What reaches here
// is the wait that ran out: a second process on the same file held the lock
// longer than the timeout, which is exactly the case worth another attempt.
func transient(err error) error {
	if held(err) {
		return fmt.Errorf("%w: %w", err, stability.ErrTransient)
	}
	return err
}
