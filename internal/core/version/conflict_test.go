package version_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localrefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/treecopy"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/version"
)

// conflictOnce lets another writer move a ref out from under the next write it
// is asked to forward, and forwards every write after that untouched.
//
// It fabricates nothing. A commit reads the ref, walks the tree, stores what it
// found and only then moves the ref, and the window between the reading and the
// moving belongs to a second process: the local store's compare-and-set is one
// step under one mutex, so a second writer in this process cannot land inside
// it. What the double controls is when the other writer runs. The refusal that
// follows comes from the real compare-and-set against the real ref, which is
// the difference between exercising the conflict path and asserting around it.
type conflictOnce struct {
	version.RefStore
	other func()
}

// Set runs the other writer, once, before the write it was armed for reaches
// the store underneath.
func (c *conflictOnce) Set(ctx context.Context, name string, expected, next version.ID) error {
	if other := c.other; other != nil {
		c.other = nil
		other()
	}
	return c.RefStore.Set(ctx, name, expected, next)
}

// The losing side of a race decided inside one commit: the ref moves after this
// commit read it and before the commit writes it. The loser is told, the
// winner's version is still what the ref stands for, the losing copy has not
// been moved onto anything, and reconciling is what makes the commit land.
func TestACommitThatLosesTheRefInsideItsOwnWindowIsToldSo(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	refs := localrefs.New(fsys, refsRoot)

	// The other writer holds the refs directly. It stands in for a second
	// process, and a second process is not writing through this one's double.
	other := version.NewVersioner(treecopy.New(fsys, "/other", "/state/other"), objects, refs)
	racing := &conflictOnce{RefStore: refs}
	mine := treecopy.New(fsys, workRoot, stateFile)
	versioner := version.NewVersioner(mine, objects, racing)

	write(t, fsys, "a.md", "the first content")
	first := commit(t, versioner, "main", "one file")
	if _, err := other.Reconcile(ctx, "main"); err != nil {
		t.Fatalf("take the other writer's tree from the ref: %v", err)
	}

	var won version.ID
	racing.other = func() {
		writeAt(t, fsys, "/other", "a.md", "the content that got there first")
		won = commit(t, other, "main", "the writer that won")
	}

	write(t, fsys, "a.md", "the content that arrived second")
	if _, err := versioner.Commit(ctx, "main", "the writer that lost"); !errors.Is(err, version.ErrConflict) {
		t.Fatalf("committing onto a ref that moved inside the commit returned %v, want version.ErrConflict", err)
	}
	if got, err := refs.Resolve(ctx, "main"); err != nil || got != won {
		t.Fatalf("main stands for %q (%v), want the winner's %q", got, err, won)
	}
	if got, err := mine.Base(ctx); err != nil || got != first {
		t.Fatalf("the losing copy stands at %q (%v), want the %q it was taken from", got, err, first)
	}

	if _, err := versioner.Reconcile(ctx, "main"); err != nil {
		t.Fatalf("reconcile after losing: %v", err)
	}
	write(t, fsys, "b.md", "committed after reconciling")
	landed, err := versioner.Commit(ctx, "main", "after reconciling")
	if err != nil {
		t.Fatalf("commit after reconciling: %v", err)
	}
	history, err := versioner.ListVersions(ctx, "main")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(history) == 0 || history[0].ID != landed || history[0].Parent != won {
		t.Errorf("the commit after reconciling is %v, want %s following the winner's %s", history, landed, won)
	}
}
