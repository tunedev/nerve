package hybridfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// A fusion claims read-only everywhere, so the suite asks it to refuse every
// mutating verb rather than asking it to store anything. That is the whole of
// what the port promises here: a fusion owns none of the keys it names.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: func(t *testing.T) vfs.FileSystem {
		return newFusion(t, ranking{})
	}})
}
