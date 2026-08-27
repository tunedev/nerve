package proxyfs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/proxyfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// A proxy has no behaviour of its own, so the suite is what says the forwarding
// is complete: every case that passes against the target has to pass against a
// proxy over a subtree of it, at names the target never sees.
func TestConformanceOverMemfs(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem {
			target := memfs.New()
			if err := target.Mkdir(t.Context(), "/sub"); err != nil {
				t.Fatalf("Mkdir(/sub) = %v", err)
			}
			return proxyfs.New(target, "/sub")
		},
	})
}

// localfs answers all three optional verb sets, so this run is what holds the
// proxy to forwarding them. Over memfs the accessor cases skip, and a proxy
// that dropped every accessor would pass a suite that never asked.
func TestConformanceOverLocalfs(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
				t.Fatalf("making the subtree = %v", err)
			}
			target, err := localfs.New(root)
			if err != nil {
				t.Fatalf("localfs.New(%q) = %v", root, err)
			}
			return proxyfs.New(target, "/sub")
		},
	})
}
