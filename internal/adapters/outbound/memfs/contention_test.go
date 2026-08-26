package memfs_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

// One tree is one mount, and a mount is reached by every request at once. Run
// under the race detector this is the evidence that the whole verb set is safe
// concurrently, not only the ones that obviously write.
func TestConcurrentVerbsOnOneTree(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	mustMkdir(t, fsys, "/jobs")

	const workers = 8
	var running sync.WaitGroup
	running.Add(workers)

	for worker := range workers {
		go func() {
			defer running.Done()

			own := fmt.Sprintf("/jobs/worker-%d", worker)
			for round := range 50 {
				content := fmt.Sprintf("round %d", round)
				if err := fsys.Write(ctx, own, []byte(content)); err != nil {
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
				if _, err := fsys.Stat(ctx, "/jobs"); err != nil {
					t.Errorf("Stat(/jobs) = %v", err)
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
