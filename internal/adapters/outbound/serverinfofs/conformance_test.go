package serverinfofs_test

import (
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/serverinfofs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return serverinfofs.New("test", time.Now, vfs.NewTraffic(time.Now)) },
	})
}
