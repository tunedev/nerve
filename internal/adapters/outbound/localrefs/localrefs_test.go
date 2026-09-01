package localrefs_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localrefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

const root = "/refs"

func newStore(t *testing.T) *localrefs.Store {
	t.Helper()
	return localrefs.New(memfs.New(), root)
}

// v is a version named the way the store now requires: the digest of something,
// rather than a string that was never one. The name it is computed from is what
// the test reads; nothing here stores content under it.
func v(name string) version.ID { return cas.Compute([]byte(name)) }

func TestResolveReportsARefThatStandsForNothing(t *testing.T) {
	store := newStore(t)

	_, err := store.Resolve(context.Background(), "main")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Resolve(main) = %v, want fs.ErrNotExist", err)
	}
}

func TestSetCreatesARefFromTheZeroID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	if err := store.Set(ctx, "main", version.ID{}, v("v1")); err != nil {
		t.Fatalf("Set(main, zero, v1) = %v", err)
	}

	got, err := store.Resolve(ctx, "main")
	if err != nil {
		t.Fatalf("Resolve(main) = %v", err)
	}
	if got != v("v1") {
		t.Errorf("Resolve(main) = %s, want v1", got)
	}
}

func TestSetRefusesToCreateARefThatAlreadyStandsForSomething(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", version.ID{}, v("v1"))

	err := store.Set(ctx, "main", version.ID{}, v("v2"))
	if !errors.Is(err, version.ErrConflict) {
		t.Fatalf("Set(main, zero, v2) = %v, want ErrConflict", err)
	}
	if got := mustResolve(t, store, "main"); got != v("v1") {
		t.Errorf("Resolve(main) = %s, want the refused write to have left v1", got)
	}
}

func TestSetMovesARefFromWhatItStandsFor(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", version.ID{}, v("v1"))

	if err := store.Set(ctx, "main", v("v1"), v("v2")); err != nil {
		t.Fatalf("Set(main, v1, v2) = %v", err)
	}
	if got := mustResolve(t, store, "main"); got != v("v2") {
		t.Errorf("Resolve(main) = %s, want v2", got)
	}
}

func TestSetRefusesAStaleExpectedID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", version.ID{}, v("v1"))
	mustSet(t, store, "main", v("v1"), v("v2"))

	err := store.Set(ctx, "main", v("v1"), v("v3"))
	if !errors.Is(err, version.ErrConflict) {
		t.Fatalf("Set(main, v1, v3) = %v, want ErrConflict", err)
	}
	if got := mustResolve(t, store, "main"); got != v("v2") {
		t.Errorf("Resolve(main) = %s, want the refused write to have left v2", got)
	}
}

// A ref pointing at nothing is a ref that does not exist, so the store refuses
// the write rather than leaving a name that resolves to the zero ID.
func TestSetRefusesTheZeroNextID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", version.ID{}, v("v1"))

	if err := store.Set(ctx, "main", v("v1"), version.ID{}); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Set(main, v1, zero) = %v, want fs.ErrInvalid", err)
	}
	if got := mustResolve(t, store, "main"); got != v("v1") {
		t.Errorf("Resolve(main) = %s, want v1", got)
	}
}

func TestDeleteUnsetsARefItStandsFor(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", version.ID{}, v("v1"))

	if err := store.Delete(ctx, "main", v("v1")); err != nil {
		t.Fatalf("Delete(main, v1) = %v", err)
	}
	if _, err := store.Resolve(ctx, "main"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Resolve(main) = %v, want fs.ErrNotExist", err)
	}
}

func TestDeleteRefusesAStaleExpectedID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "main", version.ID{}, v("v1"))

	if err := store.Delete(ctx, "main", v("v2")); !errors.Is(err, version.ErrConflict) {
		t.Fatalf("Delete(main, v2) = %v, want ErrConflict", err)
	}
	if got := mustResolve(t, store, "main"); got != v("v1") {
		t.Errorf("Resolve(main) = %s, want v1", got)
	}
}

func TestDeleteReportsARefThatStandsForNothing(t *testing.T) {
	err := newStore(t).Delete(context.Background(), "main", v("v1"))
	if !errors.Is(err, version.ErrConflict) {
		t.Fatalf("Delete(main, v1) = %v, want ErrConflict", err)
	}
}

func TestListReturnsEveryRefSorted(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	mustSet(t, store, "release", version.ID{}, v("v1"))
	mustSet(t, store, "main", version.ID{}, v("v2"))
	mustSet(t, store, "draft", version.ID{}, v("v3"))
	if err := store.Delete(ctx, "draft", v("v3")); err != nil {
		t.Fatalf("Delete(draft, v3) = %v", err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if want := []string{"main", "release"}; !slices.Equal(got, want) {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

// A store nothing has been written to yet has no root directory in the tree,
// and lists no refs rather than failing.
func TestListOfAnUnwrittenStoreIsEmpty(t *testing.T) {
	got, err := newStore(t).List(context.Background())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List() = %v, want no refs", got)
	}
}

// Every verb applies the core's name rule, so a name can never reach the tree
// and be resolved as a path.
func TestEveryVerbRefusesAnInvalidName(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	if _, err := store.Resolve(ctx, "../escape"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Resolve(../escape) = %v, want fs.ErrInvalid", err)
	}
	if err := store.Set(ctx, "../escape", version.ID{}, v("v1")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Set(../escape) = %v, want fs.ErrInvalid", err)
	}
	if err := store.Delete(ctx, "../escape", v("v1")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Delete(../escape) = %v, want fs.ErrInvalid", err)
	}
}

func TestStoreSatisfiesTheRefStorePort(t *testing.T) {
	var _ version.RefStore = newStore(t)
}

func mustSet(t *testing.T, store *localrefs.Store, name string, expected, next version.ID) {
	t.Helper()
	if err := store.Set(context.Background(), name, expected, next); err != nil {
		t.Fatalf("Set(%q, %s, %s) = %v", name, expected, next, err)
	}
}

func mustResolve(t *testing.T, store *localrefs.Store, name string) version.ID {
	t.Helper()
	got, err := store.Resolve(context.Background(), name)
	if err != nil {
		t.Fatalf("Resolve(%q) = %v", name, err)
	}
	return got
}

// Expecting the zero ID is how a ref is created, so on Delete it asks to unset
// a ref that stands for nothing. That is not a lost race; it is a missing ref.
func TestDeleteExpectingTheZeroIDIsNotExist(t *testing.T) {
	err := newStore(t).Delete(context.Background(), "main", version.ID{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Delete(main, zero) = %v, want fs.ErrNotExist", err)
	}
}

// A ref file holding something that was never a version fails here, where the
// bad value is, rather than when a reader takes it for a version and asks the
// object store for it.
func TestResolveRefusesARefThatIsNotAVersion(t *testing.T) {
	ctx := context.Background()
	fsys := memfs.New()
	store := localrefs.New(fsys, root)
	writeRef(t, fsys, "main", "not a digest")

	if _, err := store.Resolve(ctx, "main"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Resolve(main) = %v, want fs.ErrInvalid", err)
	}
}

func writeRef(t *testing.T, fsys vfs.FileSystem, name, content string) {
	t.Helper()
	ctx := context.Background()
	if err := fsys.Mkdir(ctx, root); err != nil {
		t.Fatalf("Mkdir(%q) = %v", root, err)
	}
	if err := fsys.Write(ctx, root+"/"+name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}
