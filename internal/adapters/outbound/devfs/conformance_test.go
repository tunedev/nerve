package devfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/devfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// The suite runs against the root, where the mount claims read-only, so what it
// asserts is the refusal. The sink is the one path that claims otherwise, and
// what it promises is asserted here rather than there: the suite holds a plugin
// to what it claims, and no descriptor says "swallows".
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return devfs.New(16) },
	})
}
