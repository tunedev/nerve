package vfs_test

import (
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// queueMount is the case descriptors exist for: one mount whose capability
// differs by path. Jobs are consumed by reading; the config beside them is an
// ordinary file that stays where it is.
type queueMount struct{ stub }

func (queueMount) Capabilities(path string) vfs.Caps {
	if path == "/jobs" || strings.HasPrefix(path, "/jobs/") {
		return vfs.Caps{ReadDestructive: true}
	}
	return vfs.Caps{}
}

func TestCapabilitiesResolvePerPath(t *testing.T) {
	var queue vfs.FileSystem = queueMount{}

	if !queue.Capabilities("/jobs/next").ReadDestructive {
		t.Error("job path does not report ReadDestructive")
	}

	if queue.Capabilities("/config").ReadDestructive {
		t.Error("config path reports ReadDestructive")
	}
}

// A plugin declares what is true of it and nothing else, so the zero value has
// to be the plain read-write tree every storage kind starts as.
func TestZeroCapsClaimsNothing(t *testing.T) {
	var caps vfs.Caps

	if caps.AppendOnly || caps.ReadDestructive || caps.ReadOnly || caps.Broadcast || caps.ObjectStore {
		t.Errorf("zero Caps = %+v, want every descriptor false", caps)
	}
}
