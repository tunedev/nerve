package graphfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// graphfs answers the port a mount table dispatches to, which is the only
// reason it can be mounted at all.
func TestGraphfsAnswersTheStoragePort(t *testing.T) {
	var _ vfs.FileSystem = newGraph(t, newMemStore(), statements{})
}
