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
//
// It runs once per store. The plugin is the same both times and the suite is
// the same both times: what a mount promises is not allowed to depend on which
// keyspace is under it, and running one store through it would leave that as a
// claim rather than a result.
func TestConformance(t *testing.T) {
	for name, build := range stores() {
		t.Run(name, func(t *testing.T) {
			vfstest.Run(t, vfstest.Plugin{
				New: func(t *testing.T) vfs.FileSystem { return kvfs.New(build(t), "sessions") },
			})
		})
	}
}
