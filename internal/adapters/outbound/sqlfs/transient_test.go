package sqlfs

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// The one failure here that another attempt might not see. A second holder of
// the same file keeps the write lock, the wait for it runs out, and what comes
// back has to reach the caller as retryable: the retry layer classifies, and an
// error nobody marked is never tried again.
//
// This is an internal test because it needs the lock wait as a parameter. The
// real wait is five seconds, which is a wait to prove in a test rather than
// serve out.
func TestALockThatRanOutIsRetryable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tree.db")
	ctx := t.Context()

	holder := openTree(t, file, busyTimeout)
	waiter := openTree(t, file, 0)

	tx, err := holder.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx = %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = waiter.Write(ctx, "/blocked", []byte("work"))
	if err == nil {
		t.Fatalf("Write(/blocked) against a held lock = nil, want the lock refused")
	}
	if got := vfs.Classify(err); got != vfs.Retryable {
		t.Errorf("Classify(%v) = %v, want %v", err, got, vfs.Retryable)
	}
}

// The mark is conditional, or it is not classification. A path that is not
// there will still not be there on the second attempt, and marking it transient
// would spend the retry budget on an answer.
func TestAnAnswerIsNotMarkedTransient(t *testing.T) {
	fsys := openTree(t, filepath.Join(t.TempDir(), "tree.db"), busyTimeout)

	_, err := fsys.Stat(t.Context(), "/missing")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(/missing) = %v, want %v", err, fs.ErrNotExist)
	}
	if errors.Is(err, vfs.ErrTransient) {
		t.Errorf("Stat(/missing) = %v, want it unmarked", err)
	}
	if got := vfs.Classify(err); got != vfs.FailFast {
		t.Errorf("Classify(%v) = %v, want %v", err, got, vfs.FailFast)
	}
}

// openTree opens a tree that closes with the test, so a held connection is not
// a leaked goroutine in the next one.
func openTree(t *testing.T, file string, wait time.Duration) *FS {
	t.Helper()

	fsys, err := open(file, wait)
	if err != nil {
		t.Fatalf("open(%q) = %v", file, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	return fsys
}
