package objectfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// objectfs answers the port a mount table dispatches to, which is the only
// reason it can be mounted at all.
var _ vfs.FileSystem = objectfs.New(nil)

// The descriptor is the kind rather than the entry: a bucket is an object store
// at every name, including names nothing is stored under, and answering takes
// no round trip.
func TestCapabilitiesClaimAnObjectStoreEverywhere(t *testing.T) {
	fsys := objectfs.New(newMemBucket())
	mustWrite(t, fsys, "/jobs/queued", "work")

	want := vfs.Caps{
		AppendOnly:      false,
		ReadDestructive: false,
		ReadOnly:        false,
		Broadcast:       false,
		ObjectStore:     true,
	}
	for _, name := range []string{"/", "/jobs", "/jobs/queued", "/absent"} {
		if got := fsys.Capabilities(name); got != want {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, got, want)
		}
	}
}

// A bucket hands back a body a caller reads as it arrives, so the stream verbs
// are answered. Cutting an object in place and making one name stand for
// another are not things a bucket does, so those are refused rather than
// answered with a verb set that cannot work.
func TestAccessorsAnswerTheStreamVerbsAndRefuseTheRest(t *testing.T) {
	fsys := objectfs.New(newMemBucket())

	if stream := fsys.AsStreamer(); stream == nil {
		t.Error("AsStreamer() = nil, want the plugin")
	}
	if cut := fsys.AsTruncater(); cut != nil {
		t.Errorf("AsTruncater() = %v, want nil", cut)
	}
	if link := fsys.AsSymlinker(); link != nil {
		t.Errorf("AsSymlinker() = %v, want nil", link)
	}
}
