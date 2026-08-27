package kvfs_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// kvfs answers the port a mount table dispatches to, which is the only reason
// it can be mounted at all.
var _ vfs.FileSystem = kvfs.New(kvfs.NewMemStore(), "sessions")

// The Store the plugin is written against is a port, and it has two
// implementations: the keyspace in memory, and the one over an object store.
// Both run the same cases in store_test.go, which is what makes it a port
// rather than an interface in front of one struct.
var (
	_ kvfs.Store = kvfs.NewMemStore()
	_ kvfs.Store = kvfs.NewObjectStore(nil)
)

// A keyspace is a keyspace at every path, including paths nothing is stored
// under: the descriptor is what the mount is, not what happens to be in it.
func TestCapabilitiesClaimAKeyspaceEverywhere(t *testing.T) {
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")
	mustWrite(t, fsys, "/abc/state", "running")

	for _, name := range []string{"/", "/abc", "/abc/state", "/absent"} {
		if got := fsys.Capabilities(name); got != (vfs.Caps{ObjectStore: true}) {
			t.Errorf("Capabilities(%q) = %+v, want ObjectStore alone", name, got)
		}
	}
}

// A value is fetched and replaced whole, so there is no stream over one and
// nothing to cut in place. Answering an accessor it cannot honour would mean
// inventing semantics the conformance suite would then hold every store to.
func TestKvfsAnswersNoOptionalVerbs(t *testing.T) {
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")

	if link := fsys.AsSymlinker(); link != nil {
		t.Errorf("AsSymlinker() = %v, want nil", link)
	}
	if stream := fsys.AsStreamer(); stream != nil {
		t.Errorf("AsStreamer() = %v, want nil", stream)
	}
	if cut := fsys.AsTruncater(); cut != nil {
		t.Errorf("AsTruncater() = %v, want nil", cut)
	}
}
