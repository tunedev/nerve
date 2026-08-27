package version_test

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localrefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/treecopy"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The ticket in one test: a copy taken from a version the ref has since moved
// off has diverged in both ways at once — its content is not what it was taken
// from, and what it was taken from is not where the ref stands. Committing says
// so rather than dropping the version it never saw, and reconciling resolves
// both, which is what makes the commit afterwards succeed.
func TestReconcileResolvesDivergenceBetweenTheCopyAndTheRef(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	refs := localrefs.New(fsys, refsRoot)
	mine := version.NewVersioner(treecopy.New(fsys, "/mine", "/state/mine"), objects, refs)
	theirs := version.NewVersioner(treecopy.New(fsys, "/theirs", "/state/theirs"), objects, refs)

	writeAt(t, fsys, "/mine", "a.md", "the first content")
	commit(t, mine, "main", "one file")

	// The second copy takes the tree from the ref, so it stands where the ref does.
	if _, err := theirs.Reconcile(ctx, "main"); err != nil {
		t.Fatalf("take the tree from the ref: %v", err)
	}

	// The ref moves under it, and it edits its own tree: divergence both ways.
	writeAt(t, fsys, "/mine", "a.md", "the second content")
	second := commit(t, mine, "main", "edited elsewhere")
	writeAt(t, fsys, "/theirs", "b.md", "a file only this copy has")

	if _, err := theirs.Commit(ctx, "main", "onto a ref that moved"); !errors.Is(err, version.ErrConflict) {
		t.Fatalf("committing from a copy the ref moved under returned %v, want version.ErrConflict", err)
	}

	applied, err := theirs.Reconcile(ctx, "main")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantChanges(t, applied,
		version.Change{Path: "a.md", Kind: version.Modified},
		version.Change{Path: "b.md", Kind: version.Removed},
	)
	if got := readAt(t, fsys, "/theirs", "a.md"); got != "the second content" {
		t.Errorf("a.md is %q, want what the ref stands for", got)
	}
	if _, err := fsys.Stat(ctx, "/theirs/b.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file the ref never had is still there: %v", err)
	}

	// Resolved means the copy stands where the ref does, so the commit that
	// conflicted now lands, on the version it had not seen.
	writeAt(t, fsys, "/theirs", "c.md", "committed after reconciling")
	third, err := theirs.Commit(ctx, "main", "after reconciling")
	if err != nil {
		t.Fatalf("commit after reconciling: %v", err)
	}
	history, err := theirs.ListVersions(ctx, "main")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(history) == 0 || history[0].ID != third || history[0].Parent != second {
		t.Errorf("the commit after reconciling follows %v, want %s", history, second)
	}
}

// Restore changes content and not position: a copy put back to an older version
// still stands where it stood, so committing the older content records a revert
// rather than colliding with the ref it never left.
func TestRestoreLeavesTheCopyStandingWhereItDid(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)

	write(t, fsys, "a.md", "the first content")
	first := commit(t, versioner, "main", "first")
	write(t, fsys, "a.md", "the second content")
	second := commit(t, versioner, "main", "second")

	if _, err := versioner.Restore(ctx, first); err != nil {
		t.Fatalf("restore: %v", err)
	}
	reverted, err := versioner.Commit(ctx, "main", "put the first content back")
	if err != nil {
		t.Fatalf("commit a restored version: %v", err)
	}
	if reverted == first {
		t.Errorf("the revert is the version it restored, want a version following %s", second)
	}

	history, err := versioner.ListVersions(ctx, "main")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(history) == 0 || history[0].Parent != second {
		t.Errorf("the revert follows %v, want %s", history, second)
	}
}

// A sweep computes reachability from the refs, and the copy is the one other
// place a version is named from. Stranding the copy would leave every verb that
// compares against where it stands reading an object that is gone.
func TestPurgeKeepsTheVersionTheCopyStandsOn(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	refs := localrefs.New(fsys, refsRoot)
	versioner := version.NewVersioner(treecopy.New(fsys, workRoot, stateFile), objects, refs)

	write(t, fsys, "a.md", "the only content")
	stood := commit(t, versioner, "main", "one file")
	if err := refs.Delete(ctx, "main", stood); err != nil {
		t.Fatalf("delete ref: %v", err)
	}

	if _, err := versioner.Purge(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := versioner.Diff(ctx, stood, stood); err != nil {
		t.Errorf("the version the copy stands on was swept: %v", err)
	}
}

func writeAt(t *testing.T, fsys vfs.FileSystem, root, name, content string) {
	t.Helper()

	full := path.Join(root, name)
	if err := fsys.Mkdir(context.Background(), path.Dir(full)); err != nil {
		t.Fatalf("mkdir for %q: %v", name, err)
	}
	if err := fsys.Write(context.Background(), full, []byte(content)); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

func readAt(t *testing.T, fsys vfs.FileSystem, root, name string) string {
	t.Helper()

	data, err := fsys.Read(context.Background(), path.Join(root, name))
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	return string(data)
}
