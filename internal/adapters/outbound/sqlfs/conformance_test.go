package sqlfs_test

import (
	"path/filepath"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// sqlfs claims no descriptor at any path, so it answers every case memfs does
// and skips only the verb sets it refuses. Each case gets its own database
// file: the suite's New takes the case's *testing.T for exactly this, because a
// tree in a file is a tree that outlives the case unless the case closes it.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem { return open(t) },
	})
}

// open returns a tree in a database of its own, closed when the case ends.
func open(t *testing.T) *sqlfs.FS {
	t.Helper()

	fsys, err := sqlfs.Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	return fsys
}
