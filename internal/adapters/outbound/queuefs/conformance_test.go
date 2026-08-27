package queuefs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// The queue path is read-destructive and its names are keys, so the suite drops
// every case that assumes a tree or a repeatable read and picks up the ones
// that hold a plugin to consuming what it hands back.
func TestConformanceOfTheQueue(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: newQueue, Root: "/" + queuefs.Jobs})
}

// The same plugin, one path up, claims read-only — so the same suite asks it to
// refuse every mutating verb instead of asking it to queue anything. Two runs
// against one mount is what a descriptor varying by path means in practice.
func TestConformanceOfTheMountShape(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: newQueue, Root: "/"})
}

// newQueue returns a queue with nothing in it, recorded in memory and carried
// to a broker that keeps what it is told.
func newQueue(*testing.T) vfs.FileSystem {
	journal := newMemJournal()
	return queuefs.New("test", journal, queuefs.NewRelay(journal, newMemBroker(), 0))
}
