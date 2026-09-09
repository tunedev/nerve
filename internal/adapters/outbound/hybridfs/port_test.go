package hybridfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// hybridfs answers the port a mount table dispatches to, which is the only
// reason it can be mounted at all.
func TestHybridfsAnswersTheStoragePort(t *testing.T) {
	var _ vfs.FileSystem = newFusion(t, ranking{})
}
