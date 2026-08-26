package kvfs_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
)

// One store is behind every kvfs mount in a process, and every mount is reached
// by every request at once. Run under the race detector this is the evidence
// that sharing the keyspace across mounts is safe, which is the arrangement the
// composition root builds.
func TestConcurrentVerbsOnOneStore(t *testing.T) {
	ctx := context.Background()
	store := kvfs.NewMemStore()
	sessions := kvfs.New(store, "sessions")
	agents := kvfs.New(store, "agents")

	const workers = 8
	var running sync.WaitGroup
	running.Add(workers)

	for worker := range workers {
		go func() {
			defer running.Done()

			fsys := sessions
			if worker%2 == 0 {
				fsys = agents
			}
			own := fmt.Sprintf("/worker-%d/state", worker)

			for round := range 50 {
				if err := fsys.Write(ctx, own, []byte(fmt.Sprintf("round %d", round))); err != nil {
					t.Errorf("Write(%q) = %v", own, err)
					return
				}
				if _, err := fsys.Read(ctx, own); err != nil {
					t.Errorf("Read(%q) = %v", own, err)
					return
				}
				if _, err := fsys.ReadDir(ctx, "/"); err != nil {
					t.Errorf("ReadDir(/) = %v", err)
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
}
