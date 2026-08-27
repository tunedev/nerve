package heartbeatfs_test

import (
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/heartbeatfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

func TestConformance(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return heartbeatfs.New(time.Second, time.Now) },
	})
}
