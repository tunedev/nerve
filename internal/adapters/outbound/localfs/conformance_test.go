package localfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// localfs claims no descriptor at any path, so it answers every case a plain
// read-write tree has to. It refuses nothing through an accessor either: links,
// streams and truncation are all native to a host disk, so the cases memfs
// skips run here.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem { return localfs.New(t.TempDir()) },
	})
}
