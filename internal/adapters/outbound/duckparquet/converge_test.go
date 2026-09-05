package duckparquet_test

import (
	"errors"
	"io/fs"
	"maps"
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// TestMaterialiseAgainIsANoOp is the ticket: running a projection a second time
// over records nothing has touched leaves the same files holding the same
// bytes, and running it over records that have changed leaves what those
// records say and nothing an earlier run said.
//
// It runs the real engine because convergence is a claim about the bytes on the
// tree, and the core tests make it with an encoder that writes none.
func TestMaterialiseAgainIsANoOp(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	table, mat, manifest := workspace(t, binary)

	first, err := mat.Materialise(t.Context(), manifest)
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	before := projectionTree(t, table)

	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise again: %v", err)
	}
	if after := projectionTree(t, table); !maps.Equal(before, after) {
		t.Fatalf("a second run left %d files, the first %d, and they differ", len(after), len(before))
	}

	// The one application at the offer stage goes. Nothing cuts that slice any
	// more, so the file it wrote is not a table the records still answer for.
	if err := table.RemoveAll(t.Context(), path.Join(manifest.Source.Root, "bolt")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise after a record went: %v", err)
	}

	offer := first.Slices[1]
	if _, err := table.Stat(t.Context(), offer); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(%q): %v, want fs.ErrNotExist", offer, err)
	}
	rollup := read(t, table, first.Rollup)
	if got, want := query(t, binary, rollup, "SELECT * FROM TABLE"), "applied|2|90000|2026-01-04 07:00:00+00"; got != want {
		t.Fatalf("the rollup reads back as\n%s\nwant\n%s", got, want)
	}
}

// projections is every file the materialiser wrote and what it holds.
func projectionTree(t *testing.T, fsys vfs.FileSystem) map[string]string {
	t.Helper()

	held := map[string]string{}
	entries, err := fsys.ReadDir(t.Context(), "/projections")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		at := path.Join("/projections", entry.Name)
		if entry.IsDir() {
			maps.Copy(held, slicesUnder(t, fsys, at))
			continue
		}
		held[at] = string(read(t, fsys, at))
	}
	return held
}

// slicesUnder is the files in a projection's slice directory.
func slicesUnder(t *testing.T, fsys vfs.FileSystem, dir string) map[string]string {
	t.Helper()

	entries, err := fsys.ReadDir(t.Context(), dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	held := make(map[string]string, len(entries))
	for _, entry := range entries {
		at := path.Join(dir, entry.Name)
		held[at] = string(read(t, fsys, at))
	}
	return held
}
