package duckparquet_test

import (
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/duckparquet"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/projection"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// applications is the manifest the end-to-end case projects: a workspace's
// resources, each a directory holding a meta.json, cut into slices by the stage
// they are at.
const applications = `{
  "name": "applications",
  "source": {"root": "/resources/ada/hunt", "file": "meta.json"},
  "columns": [
    {"name": "company", "type": "string", "from": "name"},
    {"name": "stage", "type": "string", "from": "field", "field": "stage"},
    {"name": "salary", "type": "int", "from": "field", "field": "salary"},
    {"name": "remote", "type": "bool", "from": "field", "field": "remote"},
    {"name": "applied_at", "type": "timestamp", "from": "field", "field": "applied_at"}
  ],
  "slice_by": "stage",
  "rollup": {"group_by": ["stage"], "measures": [{"name": "roles", "op": "count"}]}
}`

// records are the workspace this projects: three applications at two stages,
// one of them without a salary because the posting did not say.
var records = map[string]string{
	"acme": `{"stage":"applied","salary":90000,"remote":true,"applied_at":"2026-01-02T09:00:00Z"}`,
	"bolt": `{"stage":"offer","salary":120000,"remote":false,"applied_at":"2026-01-03T09:00:00Z"}`,
	"cog":  `{"stage":"applied","remote":true,"applied_at":"2026-01-04T09:00:00+02:00"}`,
}

// TestMaterialiseWritesParquetSlices is the ticket: a manifest, the records it
// names on the tree, and one Parquet file per slice that DuckDB reads back as
// the table the manifest declared.
//
// It runs the real engine over the real materialiser because that seam is the
// whole delivery. The core tests prove the slicing with an encoder that writes
// nothing; this one proves the files are Parquet.
func TestMaterialiseWritesParquetSlices(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	manifest, err := projection.Decode([]byte(applications))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	table := vfs.NewMountTable()
	if err := table.Mount("/", memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	for name, doc := range records {
		at := path.Join(manifest.Source.Root, name)
		if err := table.Mkdir(t.Context(), at); err != nil {
			t.Fatalf("Mkdir(%q): %v", at, err)
		}
		if err := table.Write(t.Context(), path.Join(at, "meta.json"), []byte(doc)); err != nil {
			t.Fatalf("Write(%q): %v", at, err)
		}
	}

	mat, err := projection.NewMaterialiser(table, duckparquet.New(binary), "/projections", time.Minute)
	if err != nil {
		t.Fatalf("NewMaterialiser: %v", err)
	}
	written, err := mat.Materialise(t.Context(), manifest)
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	want := []string{"/projections/applications/applied.parquet", "/projections/applications/offer.parquet"}
	if !slices.Equal(written, want) {
		t.Fatalf("Materialise wrote %v, want %v", written, want)
	}

	applied := read(t, table, written[0])
	if got, want := query(t, binary, applied, "SELECT * FROM TABLE ORDER BY company"), strings.Join([]string{
		"acme|applied|90000|true|2026-01-02 09:00:00+00",
		"cog|applied|NULL|true|2026-01-04 07:00:00+00",
	}, "\n"); got != want {
		t.Fatalf("the applied slice reads back as\n%s\nwant\n%s", got, want)
	}

	offer := read(t, table, written[1])
	if got, want := query(t, binary, offer, "SELECT company, salary FROM TABLE"), "bolt|120000"; got != want {
		t.Fatalf("the offer slice reads back as %q, want %q", got, want)
	}
	// The slice column is in the slice, so the file is a table on its own and
	// not one that only makes sense beside its own name.
	if got, want := query(t, binary, offer, "SELECT DISTINCT stage FROM TABLE"), "offer"; got != want {
		t.Fatalf("the offer slice holds stage %q, want %q", got, want)
	}
}

// read is what the tree holds at a path a materialise wrote.
func read(t *testing.T, fsys vfs.FileSystem, at string) []byte {
	t.Helper()

	data, err := fsys.Read(t.Context(), at)
	if err != nil {
		t.Fatalf("Read(%q): %v", at, err)
	}
	return data
}
