package memfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// memfs answers the port a mount table dispatches to, which is the only reason
// it can be mounted at all.
var _ vfs.FileSystem = memfs.New()

// memfs is a plain read-write tree: nothing is append-only, nothing is consumed
// by reading, and a directory is a real directory. It is the reference against
// which a plugin that does claim a descriptor is read.
func TestCapabilitiesClaimNothingAnywhere(t *testing.T) {
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")
	mustWrite(t, fsys, "/jobs/queued", "work")

	for _, name := range []string{"/", "/jobs", "/jobs/queued", "/absent"} {
		if got := fsys.Capabilities(name); got != (vfs.Caps{}) {
			t.Errorf("Capabilities(%q) = %+v, want the zero value", name, got)
		}
	}
}

// A tree in memory has no way to make one path stand for another, so it says so
// rather than answering with a verb set that cannot work.
func TestMemfsHasNoLinkVerbs(t *testing.T) {
	if link := memfs.New().AsSymlinker(); link != nil {
		t.Errorf("AsSymlinker() = %v, want nil", link)
	}
}

// A caller's `if s := fsys.AsStreamer(); s != nil` guard has to find the verb
// sets memfs answers, or nothing reaches them.
func TestMemfsAnswersTheStreamAndTruncateVerbs(t *testing.T) {
	fsys := memfs.New()

	if stream := fsys.AsStreamer(); stream == nil {
		t.Error("AsStreamer() = nil, want the plugin")
	}
	if cut := fsys.AsTruncater(); cut == nil {
		t.Error("AsTruncater() = nil, want the plugin")
	}
}
