package version_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/localrefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/treecopy"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/version"
)

// sweepDuring runs another party's sweep inside the window one commit opens
// between storing its content and moving the ref that will name it.
//
// The window is real and the sweep is real; what the double controls is only
// when the sweep starts. Starting it from outside would have to be timed, and a
// test that has to be timed cannot say which side of the exclusion it landed on.
type sweepDuring struct {
	version.RefStore
	sweep func()
}

// Set runs the armed sweep, once, before the write it was armed for reaches the
// store underneath.
func (s *sweepDuring) Set(ctx context.Context, name string, expected, next version.ID) error {
	if sweep := s.sweep; sweep != nil {
		s.sweep = nil
		sweep()
	}
	return s.RefStore.Set(ctx, name, expected, next)
}

// Two versioners over one object store is what a second working copy is, and
// what every multi-copy test already builds. A sweep by one has to wait for a
// commit by the other, so an exclusion held per versioner holds nothing: the
// sweep lands between the content being stored and the ref that names it, and
// the version the commit returns names an object that is gone.
func TestASweepWaitsForACommitByAnotherVersionerOverOneStore(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	refs := localrefs.New(fsys, refsRoot)

	other := version.NewVersioner(treecopy.New(fsys, "/other", "/state/other"), objects, refs)
	racing := &sweepDuring{RefStore: refs}
	versioner := version.NewVersioner(treecopy.New(fsys, workRoot, stateFile), objects, racing)

	var sweeping sync.WaitGroup
	swept := make(chan error, 1)
	racing.sweep = func() {
		sweeping.Add(1)
		go func() {
			defer sweeping.Done()
			_, err := other.Purge(ctx)
			swept <- err
		}()
		// The sweep either finishes here, which is the exclusion missing, or is
		// waiting on it, in which case waiting longer changes nothing: it cannot
		// finish until the commit holding the store returns.
		select {
		case err := <-swept:
			swept <- err
		case <-time.After(200 * time.Millisecond):
		}
	}

	const content = "stored before the ref that names it"
	write(t, fsys, "a.md", content)
	committed, err := versioner.Commit(ctx, "main", "raced by a sweep")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-swept; err != nil {
		t.Fatalf("purge: %v", err)
	}
	sweeping.Wait()

	data, err := versioner.ReadVersion(ctx, committed, "a.md")
	if err != nil {
		t.Fatalf("the sweep took the content out from under version %s: %v", committed, err)
	}
	if string(data) != content {
		t.Errorf("a.md at %s reads %q, want %q", committed, data, content)
	}
}
