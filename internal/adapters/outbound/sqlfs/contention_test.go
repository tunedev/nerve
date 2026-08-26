package sqlfs_test

import (
	"fmt"
	"sync"
	"testing"
)

// One tree is one mount, and a mount is reached by every request at once. Run
// under the race detector this is the evidence that the whole verb set is safe
// concurrently, and that contention here is a wait rather than an error: a pool
// left to open connections freely, with nothing told to wait for a lock, fails
// these same writes with SQLITE_BUSY.
func TestConcurrentVerbsOnOneTree(t *testing.T) {
	fsys, ctx := open(t), t.Context()
	if err := fsys.Mkdir(ctx, "/jobs"); err != nil {
		t.Fatalf("Mkdir(/jobs) = %v", err)
	}

	const (
		workers = 8
		rounds  = 20
	)
	var running sync.WaitGroup
	running.Add(workers)

	for worker := range workers {
		go func() {
			defer running.Done()

			own := fmt.Sprintf("/jobs/worker-%d", worker)
			for round := range rounds {
				if err := fsys.Write(ctx, own, fmt.Appendf(nil, "round %d", round)); err != nil {
					t.Errorf("Write(%q) = %v", own, err)
					return
				}
				if _, err := fsys.Read(ctx, own); err != nil {
					t.Errorf("Read(%q) = %v", own, err)
					return
				}
				if _, err := fsys.ReadDir(ctx, "/jobs"); err != nil {
					t.Errorf("ReadDir(/jobs) = %v", err)
					return
				}
				if err := fsys.Chmod(ctx, own, 0o600); err != nil {
					t.Errorf("Chmod(%q) = %v", own, err)
					return
				}
			}
			if err := fsys.Remove(ctx, own); err != nil {
				t.Errorf("Remove(%q) = %v", own, err)
			}
		}()
	}
	running.Wait()

	entries, err := fsys.ReadDir(ctx, "/jobs")
	if err != nil {
		t.Fatalf("ReadDir(/jobs) = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/jobs) = %v, want every worker's entry removed", entries)
	}
}
