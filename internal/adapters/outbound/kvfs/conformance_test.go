package kvfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// kvfs claims ObjectStore at every path, so the suite skips the cases that
// assume a tree and runs the ones that hold a prefix keyspace to what the
// descriptor means.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "sessions") },
	})
}
