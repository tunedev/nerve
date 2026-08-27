package streamfs_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
)

// One stream is written to by everything that has it mounted while readers
// follow it. Run under the race detector this is the evidence that appending
// and following are safe together; the count is the evidence that a follower
// receives every append exactly once, which is what broadcast promises.
func TestConcurrentAppendsReachAFollowerOnce(t *testing.T) {
	const (
		writers = 8
		rounds  = 50
		total   = writers * rounds
	)

	fsys := streamfs.New(total, waitFor)
	mustWrite(t, fsys, "/log", "")

	reader := mustFollow(t, fsys, "/log")
	received := readInBackground(t, reader, total)

	var running sync.WaitGroup
	running.Add(writers)
	for range writers {
		go func() {
			defer running.Done()
			for range rounds {
				if err := fsys.Write(t.Context(), "/log", []byte("x")); err != nil {
					t.Errorf("Write(/log) = %v", err)
					return
				}
			}
		}()
	}
	running.Wait()

	content := waitForRead(t, received)
	if content != strings.Repeat("x", total) {
		t.Errorf("the follower received %d bytes, want %d", len(content), total)
	}
}
