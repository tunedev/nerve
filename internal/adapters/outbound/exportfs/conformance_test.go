package exportfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/exportfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem {
			src := memfs.New()
			if err := src.Write(t.Context(), "/input", []byte("materialised\n")); err != nil {
				t.Fatalf("Write(/input) = %v", err)
			}

			fsys, err := exportfs.New(src, []exportfs.Export{{
				Name:   "report",
				Inputs: []string{"/input"},
				From:   exportfs.Join,
			}})
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			return fsys
		},
	})
}
