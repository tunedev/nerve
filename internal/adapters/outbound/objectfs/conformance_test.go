package objectfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// objectfs claims ObjectStore, so the suite drops every case that assumes a
// tree and adds the three that hold it to prefix semantics. What is left is the
// port's promise as an object store can keep it.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New:  func(*testing.T) vfs.FileSystem { return objectfs.New(newMemBucket()) },
		Root: "",
	})
}
