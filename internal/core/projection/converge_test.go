package projection_test

import (
	"errors"
	"io/fs"
	"maps"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// tree is every file under root and what it holds, which is the whole of what a
// materialise left behind: a re-run converges when two of these are equal.
func tree(t *testing.T, fsys vfs.FileSystem, root string) map[string]string {
	t.Helper()

	held := map[string]string{}
	entries, err := fsys.ReadDir(t.Context(), root)
	if errors.Is(err, fs.ErrNotExist) {
		return held
	}
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", root, err)
	}
	for _, entry := range entries {
		at := path.Join(root, entry.Name)
		if entry.IsDir() {
			maps.Copy(held, tree(t, fsys, at))
			continue
		}
		data, err := fsys.Read(t.Context(), at)
		if err != nil {
			t.Fatalf("Read(%q): %v", at, err)
		}
		held[at] = string(data)
	}
	return held
}

func TestMaterialiseAgainLeavesTheSameTree(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)

	first, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	before := tree(t, fsys, target)

	again, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise again: %v", err)
	}
	if !slices.Equal(first.Slices, again.Slices) || first.Rollup != again.Rollup {
		t.Fatalf("Materialise again returned %v, want %v", again, first)
	}
	if after := tree(t, fsys, target); !maps.Equal(before, after) {
		t.Fatalf("Materialise again left %v, want %v", after, before)
	}
}

func TestMaterialiseRemovesASliceNoRecordCutsAnyMore(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)

	if _, err := mat.Materialise(t.Context(), applications()); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	// The one record at the offer stage goes. Nothing cuts that slice now, so a
	// run over these records from nothing would not write it.
	if err := fsys.RemoveAll(t.Context(), path.Join(source, "bolt")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	got, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise again: %v", err)
	}
	if want := []string{"/projections/applications/applied.table"}; !slices.Equal(got.Slices, want) {
		t.Fatalf("Materialise again wrote %v, want %v", got.Slices, want)
	}

	stale := "/projections/applications/offer.table"
	if _, err := fsys.Stat(t.Context(), stale); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(%q): %v, want fs.ErrNotExist", stale, err)
	}
}

func TestMaterialiseRemovesTheProjectionWhenTheLastRecordGoes(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	first, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	if err := fsys.RemoveAll(t.Context(), path.Join(source, "acme")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	got, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise again: %v", err)
	}
	if len(got.Slices) != 0 || got.Rollup != "" {
		t.Fatalf("Materialise again wrote %v, want nothing", got)
	}
	for _, at := range append(first.Slices, first.Rollup, path.Join(target, "applications")) {
		if _, err := fsys.Stat(t.Context(), at); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q): %v, want fs.ErrNotExist", at, err)
		}
	}
}

func TestMaterialiseLeavesAnotherProjectionAlone(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	beside := path.Join(target, "offers", "sent.table")
	if err := fsys.Mkdir(t.Context(), path.Dir(beside)); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fsys.Write(t.Context(), beside, []byte("another projection")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := mat.Materialise(t.Context(), applications()); err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	if _, err := fsys.Stat(t.Context(), beside); err != nil {
		t.Fatalf("Stat(%q): %v", beside, err)
	}
}
