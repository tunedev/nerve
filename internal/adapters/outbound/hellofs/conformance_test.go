package hellofs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/hellofs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// hellofs claims read-only, so the suite stops asking it to store and starts
// asking it to refuse. That is the whole of what a descriptor buys: one test
// set, honoured differently.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return hellofs.New("hello") },
	})
}
