package vectorfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// The corpus is an object store: its names are keys, so the suite drops the
// cases that assume a tree and keeps the ones that hold a plugin to storing
// what it is given and handing it back unchanged.
func TestConformanceOfTheCorpus(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: newConformanceIndex, Root: "/" + vectorfs.Docs})
}

// The same plugin, one path up, claims read-only — so the same suite asks it to
// refuse every mutating verb instead of asking it to store anything. Two runs
// against one mount is what a descriptor varying by path means in practice.
func TestConformanceOfTheMountShape(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: newConformanceIndex, Root: "/"})
}

func newConformanceIndex(t *testing.T) vfs.FileSystem { return newIndex(t) }
