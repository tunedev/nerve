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
//
// It runs once per corpus. The mount promises the same thing over a directory
// on the disk and over an object store, and a promise only one of them keeps is
// not the port's — it is that store's.
func TestConformanceOfTheCorpus(t *testing.T) {
	for name, build := range corpora() {
		t.Run(name, func(t *testing.T) {
			vfstest.Run(t, vfstest.Plugin{New: over(build), Root: "/" + vectorfs.Docs})
		})
	}
}

// The same plugin, one path up, claims read-only — so the same suite asks it to
// refuse every mutating verb instead of asking it to store anything. Two runs
// against one mount is what a descriptor varying by path means in practice.
func TestConformanceOfTheMountShape(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: over(newLocalDocs), Root: "/"})
}

// over builds the mount the suite drives, over the corpus build makes.
func over(build func(*testing.T) vectorfs.DocStore) func(*testing.T) vfs.FileSystem {
	return func(t *testing.T) vfs.FileSystem {
		return newIndexOver(t, build(t), newMemIndex(), bagEmbedder{width: width})
	}
}
