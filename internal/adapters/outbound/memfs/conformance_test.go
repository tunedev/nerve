package memfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// memfs claims no descriptor at any path, so it runs every case in the suite
// that a plain read-write tree has to answer, and skips only what it refuses
// through an accessor.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return memfs.New() },
	})
}
