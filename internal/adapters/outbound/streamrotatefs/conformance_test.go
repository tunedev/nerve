package streamrotatefs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamrotatefs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// streamrotatefs claims AppendOnly and ObjectStore at every path, so the suite
// skips the cases that assume a tree or a write that replaces, and runs the
// ones that hold an append-only keyspace to what those descriptors mean.
//
// The mount is built with the default segment size, which nothing the suite
// writes comes near. What a segment being sealed does to a listing is this
// package's own to assert: the suite asks what the port promises of every kind,
// and rotation is what this kind promises on top of it.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem {
			return streamrotatefs.New(streamrotatefs.DefaultSize, streamrotatefs.DefaultKeep)
		},
	})
}
