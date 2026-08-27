package cas_test

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// counted is a mount that keeps count of the writes it was asked for. It wraps
// the reference tree rather than standing in for one, so what the store did is
// counted while what it stored is still really stored.
type counted struct {
	vfs.FileSystem
	writes atomic.Int64
}

func (c *counted) Write(ctx context.Context, name string, data []byte) error {
	c.writes.Add(1)
	return c.FileSystem.Write(ctx, name, data)
}

func newTree() *counted { return &counted{FileSystem: memfs.New()} }

// objects lists every object path beneath root, so a test can say how many
// times content was stored rather than how many times a verb was called.
func objects(t *testing.T, fsys vfs.FileSystem, root string) []string {
	t.Helper()

	entries, err := fsys.ReadDir(context.Background(), root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read_dir %q: %v", root, err)
	}

	var found []string
	for _, entry := range entries {
		name := path.Join(root, entry.Name)
		if entry.IsDir() {
			found = append(found, objects(t, fsys, name)...)
			continue
		}
		found = append(found, name)
	}
	return found
}

// The ticket in one test: content written twice is one object, and the second
// write never reaches the mount.
func TestIdenticalContentIsStoredOnce(t *testing.T) {
	tree := newTree()
	store := cas.New(tree, "/versions/objects")
	content := []byte("the same bytes from two callers")

	first, err := store.Put(context.Background(), content)
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	second, err := store.Put(context.Background(), content)
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first != second {
		t.Errorf("identical content stored as %s then %s", first, second)
	}
	if stored := objects(t, tree, "/versions/objects"); len(stored) != 1 {
		t.Errorf("identical content stored as %d objects: %v", len(stored), stored)
	}
	if writes := tree.writes.Load(); writes != 1 {
		t.Errorf("two puts of identical content made %d writes, want 1", writes)
	}
}

func TestDifferentContentIsStoredSeparately(t *testing.T) {
	tree := newTree()
	store := cas.New(tree, "/objects")

	first, err := store.Put(context.Background(), []byte("one"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	second, err := store.Put(context.Background(), []byte("two"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	if first == second {
		t.Fatalf("different content stored under one digest %s", first)
	}
	if stored := objects(t, tree, "/objects"); len(stored) != 2 {
		t.Errorf("two contents stored as %d objects: %v", len(stored), stored)
	}
}

func TestGetReturnsWhatPutStored(t *testing.T) {
	contents := map[string][]byte{
		"content":       []byte("a document, before anyone edited it"),
		"empty content": nil,
	}

	for name, content := range contents {
		t.Run(name, func(t *testing.T) {
			store := cas.New(memfs.New(), "/objects")

			digest, err := store.Put(context.Background(), content)
			if err != nil {
				t.Fatalf("put: %v", err)
			}

			got, err := store.Get(context.Background(), digest)
			if err != nil {
				t.Fatalf("get %s: %v", digest, err)
			}
			if string(got) != string(content) {
				t.Errorf("get %s = %q, want %q", digest, got, content)
			}
		})
	}
}

func TestGetOfWhatWasNeverStoredIsNotExist(t *testing.T) {
	store := cas.New(memfs.New(), "/objects")
	digest := cas.Compute([]byte("never put"))

	got, err := store.Get(context.Background(), digest)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("get %s = %q, %v, want fs.ErrNotExist", digest, got, err)
	}
}

// A digest is a promise about content, and the mount underneath is an ordinary
// tree that something else can write to. Get keeps the promise or says it
// cannot, rather than handing back a different version under the right name.
func TestGetRefusesContentThatNoLongerHashesToItsDigest(t *testing.T) {
	tree := newTree()
	store := cas.New(tree, "/objects")

	digest, err := store.Put(context.Background(), []byte("what was committed"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	stored := objects(t, tree, "/objects")
	if len(stored) != 1 {
		t.Fatalf("stored %d objects, want 1: %v", len(stored), stored)
	}
	if err := tree.Write(context.Background(), stored[0], []byte("what is there now")); err != nil {
		t.Fatalf("overwrite %q: %v", stored[0], err)
	}

	got, err := store.Get(context.Background(), digest)
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("get %s = %q, %v, want fs.ErrInvalid", digest, got, err)
	}
}

func TestHasReportsWhatIsStored(t *testing.T) {
	store := cas.New(memfs.New(), "/objects")

	absent := cas.Compute([]byte("never put"))
	switch stored, err := store.Has(context.Background(), absent); {
	case err != nil:
		t.Fatalf("has %s: %v", absent, err)
	case stored:
		t.Errorf("has %s = true before it was put", absent)
	}

	present, err := store.Put(context.Background(), []byte("put"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	switch stored, err := store.Has(context.Background(), present); {
	case err != nil:
		t.Fatalf("has %s: %v", present, err)
	case !stored:
		t.Errorf("has %s = false after it was put", present)
	}
}

func TestRemoveTakesTheObjectAndRepeatsHarmlessly(t *testing.T) {
	tree := newTree()
	store := cas.New(tree, "/objects")

	digest, err := store.Put(context.Background(), []byte("collected"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Remove(context.Background(), digest); err != nil {
		t.Fatalf("remove %s: %v", digest, err)
	}
	if err := store.Remove(context.Background(), digest); err != nil {
		t.Errorf("removing %s a second time: %v", digest, err)
	}

	if stored := objects(t, tree, "/objects"); len(stored) != 0 {
		t.Errorf("removed content left %d objects: %v", len(stored), stored)
	}
	if _, err := store.Get(context.Background(), digest); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("get after remove: %v, want fs.ErrNotExist", err)
	}
}

// Nothing serialises the store, so concurrent writers of one content both find
// it missing and both write it. They write the same bytes to the same path,
// which is what content addressing buys and why no lock is needed for it.
func TestConcurrentPutsOfIdenticalContentStoreOneObject(t *testing.T) {
	tree := newTree()
	store := cas.New(tree, "/objects")
	content := []byte("committed by everyone at once")

	const writers = 8
	digests := make([]cas.Digest, writers)
	errs := make([]error, writers)

	var group sync.WaitGroup
	for i := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			digests[i], errs[i] = store.Put(context.Background(), content)
		}()
	}
	group.Wait()

	for i := range writers {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}
		if digests[i] != digests[0] {
			t.Errorf("writer %d stored %s, writer 0 stored %s", i, digests[i], digests[0])
		}
	}
	if stored := objects(t, tree, "/objects"); len(stored) != 1 {
		t.Errorf("%d writers of one content stored %d objects: %v", writers, len(stored), stored)
	}

	got, err := store.Get(context.Background(), digests[0])
	if err != nil {
		t.Fatalf("get %s: %v", digests[0], err)
	}
	if string(got) != string(content) {
		t.Errorf("get %s = %q, want %q", digests[0], got, content)
	}
}

// The zero digest is what a caller has when it ignored an error from
// ParseDigest. It names nothing, and every verb says so rather than reaching
// into the mount with half a path.
func TestTheZeroDigestNamesNothing(t *testing.T) {
	store := cas.New(memfs.New(), "/objects")

	var zero cas.Digest
	calls := map[string]func() error{
		"get":    func() error { _, err := store.Get(context.Background(), zero); return err },
		"has":    func() error { _, err := store.Has(context.Background(), zero); return err },
		"remove": func() error { return store.Remove(context.Background(), zero) },
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s of the zero digest: %v, want fs.ErrInvalid", name, err)
			}
		})
	}
}

// Objects land under the root they were given and nowhere else, which is what
// lets a versioner keep them beside its refs in one mount.
func TestObjectsStayBeneathTheirRoot(t *testing.T) {
	tree := newTree()
	store := cas.New(tree, "workspace/objects")

	if _, err := store.Put(context.Background(), []byte("rooted")); err != nil {
		t.Fatalf("put: %v", err)
	}

	stored := objects(t, tree, "/")
	if len(stored) != 1 {
		t.Fatalf("stored %d objects, want 1: %v", len(stored), stored)
	}
	if want := "/workspace/objects/sha256/"; !strings.HasPrefix(stored[0], want) {
		t.Errorf("object stored at %q, want it beneath %q", stored[0], want)
	}
}

// The store is a shape over the port, not a storage kind: what it does is the
// same over a tree in memory and a tree on disk, where the fanout is real
// directories that have to be made before anything can be written into them.
func TestTheSameStoreWorksOverEitherMount(t *testing.T) {
	disk, err := localfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}

	mounts := map[string]vfs.FileSystem{"in memory": memfs.New(), "on disk": disk}

	for name, fsys := range mounts {
		t.Run(name, func(t *testing.T) {
			store := cas.New(fsys, "/objects")
			content := []byte("stored wherever the mount keeps it")

			digest, err := store.Put(context.Background(), content)
			if err != nil {
				t.Fatalf("put: %v", err)
			}
			if _, err := store.Put(context.Background(), content); err != nil {
				t.Fatalf("second put: %v", err)
			}

			got, err := store.Get(context.Background(), digest)
			if err != nil {
				t.Fatalf("get %s: %v", digest, err)
			}
			if string(got) != string(content) {
				t.Errorf("get %s = %q, want %q", digest, got, content)
			}
			if stored := objects(t, fsys, "/objects"); len(stored) != 1 {
				t.Errorf("stored %d objects, want 1: %v", len(stored), stored)
			}
		})
	}
}
