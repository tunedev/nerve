package localfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// localfs claims no descriptor at any path, so it answers every case a plain
// read-write tree has to. It refuses nothing through an accessor either: links,
// streams and truncation are all native to a host disk, so the cases memfs
// skips run here.
//
// It hands over the host directory rather than a filesystem it made, which is
// what makes the escape cases run: the suite plants an entry outside that
// directory and a link inside it, and holds the mount to reaching neither.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		Host: func(t *testing.T, dir string) vfs.FileSystem { return mustNew(t, dir) },
	})
}
