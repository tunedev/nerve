package httpfs_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// The origin serves nothing, which is enough: what the suite asks a read-only
// object store is that it refuses mutation and reports a missing path as
// missing, and both are answers an empty origin gives.
func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem {
			return mustNew(t, httptest.NewServer(http.NotFoundHandler()))
		},
	})
}
