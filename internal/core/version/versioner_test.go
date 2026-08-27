package version_test

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/localrefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// tree is where a versioner keeps everything: the working copy under /work, the
// objects under /versions/objects, the refs under /versions/refs. One mount
// holds all three, which is what makes a versioner over memfs and a versioner
// over a host directory the same code.
const (
	workRoot    = "/work"
	objectsRoot = "/versions/objects"
	refsRoot    = "/versions/refs"
)

// mounts are the trees the versioner is asserted over. Nothing in it knows
// which one it holds, so the round trip has to hold on both.
func mounts(t *testing.T) map[string]vfs.FileSystem {
	t.Helper()

	disk, err := localfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("localfs: %v", err)
	}
	return map[string]vfs.FileSystem{"memfs": memfs.New(), "localfs": disk}
}

func newVersioner(t *testing.T, fsys vfs.FileSystem) *version.Versioner {
	t.Helper()
	return version.NewVersioner(fsys, workRoot, cas.New(fsys, objectsRoot), localrefs.New(fsys, refsRoot))
}

func write(t *testing.T, fsys vfs.FileSystem, name, content string) {
	t.Helper()

	full := path.Join(workRoot, name)
	if err := fsys.Mkdir(context.Background(), path.Dir(full)); err != nil {
		t.Fatalf("mkdir for %q: %v", name, err)
	}
	if err := fsys.Write(context.Background(), full, []byte(content)); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

func remove(t *testing.T, fsys vfs.FileSystem, name string) {
	t.Helper()

	if err := fsys.Remove(context.Background(), path.Join(workRoot, name)); err != nil {
		t.Fatalf("remove %q: %v", name, err)
	}
}

func read(t *testing.T, fsys vfs.FileSystem, name string) string {
	t.Helper()

	data, err := fsys.Read(context.Background(), path.Join(workRoot, name))
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	return string(data)
}

func commit(t *testing.T, v *version.Versioner, ref, message string) version.ID {
	t.Helper()

	id, err := v.Commit(context.Background(), ref, message)
	if err != nil {
		t.Fatalf("commit %q: %v", message, err)
	}
	return id
}

// wantChanges compares a diff with what it should have said, in the order a
// diff reports: by path.
func wantChanges(t *testing.T, got []version.Change, want ...version.Change) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d changes %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d is %v, want %v", i, got[i], want[i])
		}
	}
}

// The ticket in one test, over every mount: commit a tree, mutate it, ask what
// changed, and put it back.
func TestCommitMutateDiffRestore(t *testing.T) {
	for name, fsys := range mounts(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			versioner := newVersioner(t, fsys)

			write(t, fsys, "notes/one.md", "the first note")
			write(t, fsys, "two.md", "the second note")
			committed := commit(t, versioner, "main", "two notes")

			write(t, fsys, "notes/one.md", "the first note, edited")
			write(t, fsys, "three.md", "a third note")
			remove(t, fsys, "two.md")
			mutated := commit(t, versioner, "main", "edited, added, removed")

			difference, err := versioner.Diff(ctx, committed, mutated)
			if err != nil {
				t.Fatalf("diff: %v", err)
			}
			wantChanges(t, difference,
				version.Change{Path: "notes/one.md", Kind: version.Modified},
				version.Change{Path: "three.md", Kind: version.Added},
				version.Change{Path: "two.md", Kind: version.Removed},
			)

			if _, err := versioner.Restore(ctx, committed); err != nil {
				t.Fatalf("restore: %v", err)
			}
			if got := read(t, fsys, "notes/one.md"); got != "the first note" {
				t.Errorf("restored note is %q, want the content it was committed with", got)
			}
			if got := read(t, fsys, "two.md"); got != "the second note" {
				t.Errorf("restored note is %q, want the content it was committed with", got)
			}
			if _, err := fsys.Stat(ctx, path.Join(workRoot, "three.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a file the restored version never had is still there: %v", err)
			}

			status, err := versioner.Status(ctx, "main")
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			wantChanges(t, status,
				version.Change{Path: "notes/one.md", Kind: version.Modified},
				version.Change{Path: "three.md", Kind: version.Removed},
				version.Change{Path: "two.md", Kind: version.Added},
			)
		})
	}
}

// An unset ref stands for the zero version, so the first status of a tree says
// what is new rather than that the ref is missing.
func TestStatusBeforeTheFirstCommitSaysEveryFileIsNew(t *testing.T) {
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")
	write(t, fsys, "deep/b.md", "two")

	status, err := versioner.Status(context.Background(), "main")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	wantChanges(t, status,
		version.Change{Path: "a.md", Kind: version.Added},
		version.Change{Path: "deep/b.md", Kind: version.Added},
	)
}

func TestStatusIsEmptyRightAfterACommit(t *testing.T) {
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")
	commit(t, versioner, "main", "one file")

	status, err := versioner.Status(context.Background(), "main")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	wantChanges(t, status)
}

// Status answers a question. A question that stored objects would leave a sweep
// to clean up after being asked.
func TestStatusStoresNothing(t *testing.T) {
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	versioner := version.NewVersioner(fsys, workRoot, objects, localrefs.New(fsys, refsRoot))
	write(t, fsys, "a.md", "never committed")

	if _, err := versioner.Status(context.Background(), "main"); err != nil {
		t.Fatalf("status: %v", err)
	}
	stored, err := objects.List(context.Background())
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("status stored %d objects, want none", len(stored))
	}
}

// Running a commit twice is a no-op, which is what makes it safe to run from
// something that cannot tell whether it already ran.
func TestCommittingAnUnchangedTreeMakesNoSecondVersion(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")

	first := commit(t, versioner, "main", "one file")
	second := commit(t, versioner, "main", "one file again")

	if first != second {
		t.Errorf("an unchanged tree committed as %s then %s", first, second)
	}
	history, err := versioner.ListVersions(ctx, "main")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("two commits of one tree made %d versions, want 1", len(history))
	}
}

func TestListVersionsIsNewestFirst(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)

	write(t, fsys, "a.md", "one")
	first := commit(t, versioner, "main", "first")
	write(t, fsys, "a.md", "two")
	second := commit(t, versioner, "main", "second")

	history, err := versioner.ListVersions(ctx, "main")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history is %d versions, want 2", len(history))
	}
	if history[0].ID != second || history[1].ID != first {
		t.Errorf("history is %s then %s, want %s then %s", history[0].ID, history[1].ID, second, first)
	}
	if history[0].Parent != first {
		t.Errorf("the second version follows %s, want %s", history[0].Parent, first)
	}
	if history[1].Parent != "" {
		t.Errorf("the first version follows %s, want nothing", history[1].Parent)
	}
	if history[0].Message != "second" {
		t.Errorf("the second version says %q, want %q", history[0].Message, "second")
	}
	if history[0].Time.IsZero() {
		t.Error("a version was recorded with no time")
	}
}

func TestListVersionsOfAnUnsetRefIsEmpty(t *testing.T) {
	versioner := newVersioner(t, memfs.New())

	history, err := versioner.ListVersions(context.Background(), "main")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("an unset ref has %d versions, want none", len(history))
	}
}

func TestReadVersionReturnsWhatThePathHeldThen(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)

	write(t, fsys, "notes/a.md", "before")
	before := commit(t, versioner, "main", "before")
	write(t, fsys, "notes/a.md", "after")
	after := commit(t, versioner, "main", "after")

	for _, tc := range []struct {
		id   version.ID
		name string
		want string
	}{
		{id: before, name: "notes/a.md", want: "before"},
		{id: before, name: "/notes/a.md", want: "before"},
		{id: after, name: "notes/a.md", want: "after"},
	} {
		data, err := versioner.ReadVersion(ctx, tc.id, tc.name)
		if err != nil {
			t.Fatalf("read %q at %s: %v", tc.name, tc.id, err)
		}
		if string(data) != tc.want {
			t.Errorf("%q at %s is %q, want %q", tc.name, tc.id, data, tc.want)
		}
	}
}

func TestReadVersionOfAPathTheVersionNeverHad(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")
	id := commit(t, versioner, "main", "one file")

	if _, err := versioner.ReadVersion(ctx, id, "b.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading a path the version never had returned %v, want fs.ErrNotExist", err)
	}
}

// Comparing against nothing is a comparison; restoring to nothing would be a
// deletion of the whole tree wearing a restore's name.
func TestRestoreRefusesTheZeroVersion(t *testing.T) {
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")

	if _, err := versioner.Restore(context.Background(), ""); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("restoring the zero version returned %v, want fs.ErrInvalid", err)
	}
	if got := read(t, fsys, "a.md"); got != "one" {
		t.Errorf("the refused restore changed the tree: a.md is %q", got)
	}
}

func TestDiffFromTheZeroVersionIsEveryPathAdded(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")
	write(t, fsys, "deep/b.md", "two")
	id := commit(t, versioner, "main", "two files")

	difference, err := versioner.Diff(ctx, "", id)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	wantChanges(t, difference,
		version.Change{Path: "a.md", Kind: version.Added},
		version.Change{Path: "deep/b.md", Kind: version.Added},
	)
}

// Divergence is resolved one way: the ref wins, and what comes back is what the
// working tree gave up.
func TestReconcileMakesTheTreeStandWhereTheRefDoes(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)

	write(t, fsys, "a.md", "committed")
	commit(t, versioner, "main", "one file")
	write(t, fsys, "a.md", "edited but never committed")
	write(t, fsys, "b.md", "never committed at all")

	applied, err := versioner.Reconcile(ctx, "main")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantChanges(t, applied,
		version.Change{Path: "a.md", Kind: version.Modified},
		version.Change{Path: "b.md", Kind: version.Removed},
	)
	if got := read(t, fsys, "a.md"); got != "committed" {
		t.Errorf("a.md is %q, want what the ref stands for", got)
	}
	status, err := versioner.Status(ctx, "main")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	wantChanges(t, status)
}

func TestReconcileOfARefStandingForNothing(t *testing.T) {
	fsys := memfs.New()
	versioner := newVersioner(t, fsys)
	write(t, fsys, "a.md", "one")

	if _, err := versioner.Reconcile(context.Background(), "main"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reconciling to an unset ref returned %v, want fs.ErrNotExist", err)
	}
	if got := read(t, fsys, "a.md"); got != "one" {
		t.Errorf("the refused reconcile emptied the tree: a.md is %q", got)
	}
}

// Purge sweeps by reachability, so what it removes is what nothing behind a ref
// names, and what it keeps is still readable afterwards.
func TestPurgeRemovesWhatNoVersionNames(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	refs := localrefs.New(fsys, refsRoot)
	versioner := version.NewVersioner(fsys, workRoot, objects, refs)

	write(t, fsys, "a.md", "the first content")
	first := commit(t, versioner, "main", "first")
	write(t, fsys, "a.md", "the second content")
	commit(t, versioner, "main", "second")

	loose, err := objects.Put(ctx, []byte("stored by a commit that never finished"))
	if err != nil {
		t.Fatalf("put loose object: %v", err)
	}

	removed, err := versioner.Purge(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed != 1 {
		t.Errorf("purge removed %d objects, want 1", removed)
	}
	if held, err := objects.Has(ctx, loose); err != nil || held {
		t.Errorf("an object no version names is still stored (%v, %v)", held, err)
	}

	// History behind the ref is reachable, so the version before the current
	// one still reads.
	data, err := versioner.ReadVersion(ctx, first, "a.md")
	if err != nil {
		t.Fatalf("read the version before the current one: %v", err)
	}
	if string(data) != "the first content" {
		t.Errorf("the swept-over version reads %q, want %q", data, "the first content")
	}
}

func TestPurgeRemovesAVersionNoRefLeadsTo(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	objects := cas.New(fsys, objectsRoot)
	refs := localrefs.New(fsys, refsRoot)
	versioner := version.NewVersioner(fsys, workRoot, objects, refs)

	write(t, fsys, "a.md", "kept")
	commit(t, versioner, "main", "kept")
	write(t, fsys, "a.md", "abandoned")
	abandoned := commit(t, versioner, "scratch", "abandoned")

	if err := refs.Delete(ctx, "scratch", abandoned); err != nil {
		t.Fatalf("delete ref: %v", err)
	}
	if _, err := versioner.Purge(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := versioner.ReadVersion(ctx, abandoned, "a.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a version no ref leads to still reads: %v", err)
	}
	if _, err := versioner.ListVersions(ctx, "main"); err != nil {
		t.Errorf("the kept history did not survive the sweep: %v", err)
	}
}

func TestPurgeOfAStoreWithNothingInIt(t *testing.T) {
	versioner := newVersioner(t, memfs.New())

	removed, err := versioner.Purge(context.Background())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed != 0 {
		t.Errorf("purge removed %d objects from an empty store, want 0", removed)
	}
}

// stale is a ref store that reports a ref standing for nothing while it really
// stands for a version. It is what a commit looks like from the losing side of
// a race: what was read is no longer what is there, and the compare-and-set is
// the only thing that can say so.
type stale struct {
	version.RefStore
}

func (stale) Resolve(context.Context, string) (version.ID, error) {
	return "", fs.ErrNotExist
}

func TestCommitOntoARefThatMovedIsAConflict(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	refs := localrefs.New(fsys, refsRoot)
	versioner := version.NewVersioner(fsys, workRoot, cas.New(fsys, objectsRoot), refs)

	write(t, fsys, "a.md", "one")
	if _, err := versioner.Commit(ctx, "main", "one file"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	blind := version.NewVersioner(fsys, workRoot, cas.New(fsys, objectsRoot), stale{refs})
	write(t, fsys, "a.md", "two")
	if _, err := blind.Commit(ctx, "main", "racing"); !errors.Is(err, version.ErrConflict) {
		t.Fatalf("committing onto a ref that moved returned %v, want version.ErrConflict", err)
	}
}
