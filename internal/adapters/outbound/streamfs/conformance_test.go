package streamfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// streamfs claims AppendOnly, Broadcast and ObjectStore at every path, so the
// suite skips the cases that assume a tree or a write that replaces, and runs
// the ones that hold an append-only keyspace to what those descriptors mean.
//
// The mount is built to wait for nothing: a follower that waits for appends
// nobody is making would only make the suite slow, and what a reader does while
// it waits is asserted in this package's own tests.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return streamfs.New(streamfs.DefaultCapacity, 0) },
	})
}
