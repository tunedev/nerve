package localrefs_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tunedev/nerve/internal/core/version"
)

// Every writer read the same ref and every one of them wants to move it. Run
// under the race detector this is the evidence that compare-and-set is one
// step: exactly one writer wins, the rest are told they lost, and the ref
// stands for what the winner wrote rather than for the last write to land.
func TestOnlyOneWriterMovesARef(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", "", "v1")

	const writers = 8
	var (
		running sync.WaitGroup
		won     atomic.Int64
		winner  atomic.Value
	)
	running.Add(writers)

	for writer := range writers {
		go func() {
			defer running.Done()

			next := version.ID(fmt.Sprintf("v1-from-%d", writer))
			switch err := store.Set(ctx, "main", "v1", next); {
			case err == nil:
				won.Add(1)
				winner.Store(next)
			case errors.Is(err, version.ErrConflict):
			default:
				t.Errorf("Set(main, v1, %q) = %v, want nil or ErrConflict", next, err)
			}
		}()
	}
	running.Wait()

	if got := won.Load(); got != 1 {
		t.Fatalf("%d writers moved the ref, want exactly 1", got)
	}
	if got, want := mustResolve(t, store, "main"), winner.Load(); got != want {
		t.Errorf("Resolve(main) = %q, want the winner's %q", got, want)
	}
}
