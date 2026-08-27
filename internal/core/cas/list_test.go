package cas_test

import (
	"context"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/cas"
)

// A sweep decides an object is referred to by nothing, so what it is handed has
// to be every object and nothing else.
func TestListReturnsEveryStoredDigest(t *testing.T) {
	ctx := context.Background()
	tree := memfs.New()
	store := cas.New(tree, "/objects")

	var want []cas.Digest
	for _, content := range [][]byte{[]byte("one"), []byte("two"), []byte("three"), nil} {
		digest, err := store.Put(ctx, content)
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		want = append(want, digest)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("list returned %d digests, want %d", len(got), len(want))
	}
	for _, digest := range want {
		if !slices.Contains(got, digest) {
			t.Errorf("list did not return %s", digest)
		}
	}
}

func TestListOfAStoreNothingWasEverPutIn(t *testing.T) {
	got, err := cas.New(memfs.New(), "/objects").List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a store with nothing in it listed %d digests", len(got))
	}
}

func TestListDoesNotReturnWhatItRemoved(t *testing.T) {
	ctx := context.Background()
	store := cas.New(memfs.New(), "/objects")

	kept, err := store.Put(ctx, []byte("kept"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	swept, err := store.Put(ctx, []byte("swept"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Remove(ctx, swept); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !slices.Equal(got, []cas.Digest{kept}) {
		t.Errorf("list returned %v, want only %s", got, kept)
	}
}

// The store shares its mount with whatever else was handed the same root, so a
// sweep that failed on a stray file would be a sweep that never ran.
func TestListSkipsWhatIsNotLaidOutAsAnObject(t *testing.T) {
	ctx := context.Background()
	tree := memfs.New()
	store := cas.New(tree, "/objects")

	digest, err := store.Put(ctx, []byte("an object"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	for _, stray := range []string{
		"/objects/README",
		"/objects/sha256/README",
		"/objects/sha256/zz/not-hex",
		"/objects/sha256/three-digits/00ff",
	} {
		if err := tree.Mkdir(ctx, path.Dir(stray)); err != nil {
			t.Fatalf("mkdir for %q: %v", stray, err)
		}
		if err := tree.Write(ctx, stray, []byte("not an object")); err != nil {
			t.Fatalf("write %q: %v", stray, err)
		}
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !slices.Equal(got, []cas.Digest{digest}) {
		t.Errorf("list returned %v, want only %s", got, digest)
	}
}
