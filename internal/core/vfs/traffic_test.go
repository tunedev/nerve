package vfs_test

import (
	"errors"
	"io/fs"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// call is what one report through a Traffic looks like from a test: begin it,
// end it with err.
func call(t *testing.T, traffic *vfs.Traffic, mount, kind string, err error) {
	_, done := traffic.Begin(t.Context(), vfs.Call{Mount: mount, Kind: kind, Verb: "Read", Path: "/x"})
	done(err)
}

func TestTrafficCountsEachMountSeparately(t *testing.T) {
	t.Parallel()

	traffic := vfs.NewTraffic(time.Now)
	call(t, traffic, "/local", "localfs", nil)
	call(t, traffic, "/local", "localfs", nil)
	call(t, traffic, "/mem", "memfs", nil)

	want := []vfs.Tally{
		{Mount: "/local", Kind: "localfs", Calls: 2},
		{Mount: "/mem", Kind: "memfs", Calls: 1},
	}
	if got := traffic.Tallies(); !slices.Equal(got, want) {
		t.Errorf("Tallies() = %v, want %v", got, want)
	}
}

// A failure is a call that happened, so it counts twice: once as traffic and
// once as a failure. A mount answering nothing but errors is still being asked.
func TestTrafficCountsFailuresAsCallsToo(t *testing.T) {
	t.Parallel()

	traffic := vfs.NewTraffic(time.Now)
	call(t, traffic, "/local", "localfs", nil)
	call(t, traffic, "/local", "localfs", fs.ErrNotExist)

	want := []vfs.Tally{{Mount: "/local", Kind: "localfs", Calls: 2, Failed: 1}}
	if got := traffic.Tallies(); !slices.Equal(got, want) {
		t.Errorf("Tallies() = %v, want %v", got, want)
	}
}

// The tally is read as a document, so the order it reads in has to be the same
// twice running rather than whatever a map iterated.
func TestTalliesAreSortedByMount(t *testing.T) {
	t.Parallel()

	traffic := vfs.NewTraffic(time.Now)
	for _, mount := range []string{"/z", "/a", "/m"} {
		call(t, traffic, mount, "memfs", nil)
	}

	mounts := make([]string, 0, 3)
	for _, tally := range traffic.Tallies() {
		mounts = append(mounts, tally.Mount)
	}
	if want := []string{"/a", "/m", "/z"}; !slices.Equal(mounts, want) {
		t.Errorf("Tallies() mounts = %v, want %v", mounts, want)
	}
}

// A call is counted when it arrives rather than when it returns, so a mount
// that has stopped answering reads as a mount being called rather than as one
// nobody is using.
func TestACallInFlightIsAlreadyCounted(t *testing.T) {
	t.Parallel()

	traffic := vfs.NewTraffic(time.Now)
	_, done := traffic.Begin(t.Context(), vfs.Call{Mount: "/local", Kind: "localfs", Verb: "Read"})

	want := []vfs.Tally{{Mount: "/local", Kind: "localfs", Calls: 1}}
	if got := traffic.Tallies(); !slices.Equal(got, want) {
		t.Errorf("Tallies() mid-call = %v, want %v", got, want)
	}
	done(nil)
}

// Every mount is called from whatever is calling the tree, so the tally is
// written from every goroutine at once. Under -race this is the check that it
// may be.
func TestConcurrentCallsAreAllCounted(t *testing.T) {
	t.Parallel()

	traffic := vfs.NewTraffic(time.Now)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				call(t, traffic, "/local", "localfs", nil)
				call(t, traffic, "/mem", "memfs", errors.New("no"))
			}
		}()
	}
	wg.Wait()

	want := []vfs.Tally{
		{Mount: "/local", Kind: "localfs", Calls: 800},
		{Mount: "/mem", Kind: "memfs", Calls: 800, Failed: 800},
	}
	if got := traffic.Tallies(); !slices.Equal(got, want) {
		t.Errorf("Tallies() = %v, want %v", got, want)
	}
}

// The tally is an entry in the tree, and an entry that never changes its
// modification time reads as stale. It changes when a call does.
func TestChangedIsWhenTheLastCallEnded(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()
	traffic := vfs.NewTraffic(func() time.Time { return at })

	if got := traffic.Changed(); !got.Equal(at) {
		t.Errorf("Changed() before any call = %v, want %v", got, at)
	}

	at = at.Add(time.Minute)
	call(t, traffic, "/local", "localfs", nil)

	if got := traffic.Changed(); !got.Equal(at) {
		t.Errorf("Changed() after a call = %v, want %v", got, at)
	}
}
