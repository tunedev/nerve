package projection_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/projection"
	"github.com/tunedev/nerve/internal/core/vfs"
)

const (
	source = "/resources/ada/hunt"
	target = "/projections"
)

// applications is the manifest every case below projects unless it says
// otherwise: a column read from the record's name, three read from its
// document, and slices cut by the one that is a stage.
func applications() projection.Manifest {
	return projection.Manifest{
		Name:   "applications",
		Source: projection.Source{Root: source, File: "meta.json"},
		Columns: []projection.Column{
			{Name: "id", Type: projection.String, From: projection.FromName},
			{Name: "stage", Type: projection.String, From: projection.FromField, Field: "stage"},
			{Name: "salary", Type: projection.Int, From: projection.FromField, Field: "salary"},
			{Name: "remote", Type: projection.Bool, From: projection.FromField, Field: "remote"},
		},
		SliceBy: "stage",
		Rollup:  projection.Rollup{},
	}
}

// recorder is an encoder that writes nothing real and remembers every table it
// was asked to write, so a core test asserts the rows a slice carried without
// an engine anywhere near it.
//
// A materialise encodes the slices in order and the rollup last, so the tables
// line up with the slices and the last one is the combined table.
type recorder struct {
	schemas []projection.Schema
	tables  [][]projection.Row
	fail    error
}

func (*recorder) Extension() string { return "table" }

func (r *recorder) Encode(_ context.Context, schema projection.Schema, rows []projection.Row) ([]byte, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	r.schemas = append(r.schemas, schema)
	r.tables = append(r.tables, rows)
	return fmt.Appendf(nil, "table of %d", len(rows)), nil
}

// rollup is the last table the recorder was given, which is the combined one.
func (r *recorder) rollup() []projection.Row { return r.tables[len(r.tables)-1] }

// materialiser is a projection writer over an empty tree, with the recorder it
// encodes through and the filesystem both ends sit on.
func materialiser(t *testing.T) (*projection.Materialiser, *recorder, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount("/", memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	encoder := &recorder{schemas: nil, tables: nil, fail: nil}
	mat, err := projection.NewMaterialiser(table, encoder, target, time.Minute)
	if err != nil {
		t.Fatalf("NewMaterialiser: %v", err)
	}
	return mat, encoder, table
}

// writeRecord puts one record directory holding doc under the manifest source.
func writeRecord(t *testing.T, fsys vfs.FileSystem, name, doc string) {
	t.Helper()

	ctx := t.Context()
	at := path.Join(source, name)
	if err := fsys.Mkdir(ctx, at); err != nil {
		t.Fatalf("Mkdir(%q): %v", at, err)
	}
	if err := fsys.Write(ctx, path.Join(at, "meta.json"), []byte(doc)); err != nil {
		t.Fatalf("Write(%q): %v", at, err)
	}
}

func TestMaterialiseWritesOneFilePerSlice(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)
	writeRecord(t, fsys, "cog", `{"stage":"applied","salary":85000,"remote":true}`)

	got, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	want := []string{"/projections/applications/applied.table", "/projections/applications/offer.table"}
	if !slices.Equal(got.Slices, want) {
		t.Fatalf("Materialise wrote %v, want %v", got.Slices, want)
	}
	for _, at := range append(got.Slices, got.Rollup) {
		if _, err := fsys.Stat(t.Context(), at); err != nil {
			t.Errorf("Stat(%q): %v", at, err)
		}
	}

	// The applied slice carries both records that were in it, in the order the
	// tree listed them, and the offer slice carries the one that was not.
	applied := [][]projection.Value{
		{"acme", "applied", int64(90000), true},
		{"cog", "applied", int64(85000), true},
	}
	assertTable(t, encoder.tables[0], applied)
	assertTable(t, encoder.tables[1], [][]projection.Value{{"bolt", "offer", int64(120000), false}})
}

func TestMaterialiseEncodesTheSliceSchema(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	if _, err := mat.Materialise(t.Context(), applications()); err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	if want := applications().SliceSchema(); !slices.Equal(encoder.schemas[0], want) {
		t.Fatalf("encoded schema %v, want %v", encoder.schemas[0], want)
	}
}

func TestMaterialiseWritesWhatTheEncoderReturned(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	got, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	data, err := fsys.Read(t.Context(), got.Slices[0])
	if err != nil {
		t.Fatalf("Read(%q): %v", got.Slices[0], err)
	}
	if string(data) != "table of 1" {
		t.Fatalf("slice holds %q, want the encoder's bytes", data)
	}
}

func TestMaterialisePassesOverWhatIsNotARecord(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	// A directory with no record file, and a file where a record directory
	// would be. Neither is a record, and neither is an error.
	if err := fsys.Mkdir(t.Context(), path.Join(source, "scratch")); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fsys.Write(t.Context(), path.Join(source, "notes.txt"), []byte("loose")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := mat.Materialise(t.Context(), applications()); err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	assertTable(t, encoder.tables[0], [][]projection.Value{{"acme", "applied", int64(90000), true}})
}

func TestMaterialiseWritesNothingForNoRecords(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	if err := fsys.Mkdir(t.Context(), source); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	got, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	if len(got.Slices) != 0 || got.Rollup != "" {
		t.Fatalf("Materialise wrote %v, want nothing", got)
	}
	if len(encoder.tables) != 0 {
		t.Fatalf("encoder wrote %d tables, want none", len(encoder.tables))
	}
	if _, err := fsys.Stat(t.Context(), path.Join(target, "applications")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat of the projection directory: %v, want fs.ErrNotExist", err)
	}
}

func TestMaterialiseRefusesASourceThatIsNotThere(t *testing.T) {
	t.Parallel()

	mat, _, _ := materialiser(t)
	if _, err := mat.Materialise(t.Context(), applications()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Materialise over a source nothing is under: %v, want fs.ErrNotExist", err)
	}
}

func TestMaterialiseRefusesAManifestThatDoesNotValidate(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied"}`)

	broken := applications()
	broken.SliceBy = "phase"
	if _, err := mat.Materialise(t.Context(), broken); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Materialise of a manifest slicing by a column it lacks: %v, want fs.ErrInvalid", err)
	}
}

func TestMaterialiseReportsWhatTheEncoderRefused(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	encoder.fail = errors.New("no engine")
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	_, err := mat.Materialise(t.Context(), applications())
	if err == nil || !errors.Is(err, encoder.fail) {
		t.Fatalf("Materialise with a failing encoder: %v, want the encoder's error", err)
	}
}

func TestNewMaterialiserRefusesWhatCannotWrite(t *testing.T) {
	t.Parallel()

	table := vfs.NewMountTable()
	encoder := &recorder{schemas: nil, tables: nil, fail: nil}
	for _, root := range []string{"", "projections", "/projections/", "/projections/../elsewhere"} {
		if _, err := projection.NewMaterialiser(table, encoder, root, time.Minute); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewMaterialiser(%q): %v, want fs.ErrInvalid", root, err)
		}
	}
	if _, err := projection.NewMaterialiser(nil, encoder, target, time.Minute); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewMaterialiser with no filesystem: %v, want fs.ErrInvalid", err)
	}
	if _, err := projection.NewMaterialiser(table, nil, target, time.Minute); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewMaterialiser with no encoder: %v, want fs.ErrInvalid", err)
	}
	if _, err := projection.NewMaterialiser(table, encoder, target, 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewMaterialiser with no timeout: %v, want fs.ErrInvalid", err)
	}
}

// assertTable compares the rows a slice carried against what the records said.
func assertTable(t *testing.T, got []projection.Row, want [][]projection.Value) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("slice holds %d rows, want %d", len(got), len(want))
	}
	for i, row := range got {
		if !slices.Equal([]projection.Value(row), want[i]) {
			t.Errorf("row %d is %v, want %v", i, row, want[i])
		}
	}
}

// mustTime is an instant a test asserts a timestamp column read.
func mustTime(t *testing.T, text string) time.Time {
	t.Helper()

	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return at.UTC()
}

// blocker is an encoder that waits for its context instead of encoding, so a
// test can ask whether an encode is bounded at all.
type blocker struct{}

func (blocker) Extension() string { return "table" }

func (blocker) Encode(ctx context.Context, _ projection.Schema, _ []projection.Row) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestMaterialiseBoundsEveryEncode(t *testing.T) {
	t.Parallel()

	table := vfs.NewMountTable()
	if err := table.Mount("/", memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	mat, err := projection.NewMaterialiser(table, blocker{}, target, time.Millisecond)
	if err != nil {
		t.Fatalf("NewMaterialiser: %v", err)
	}
	writeRecord(t, table, "acme", `{"stage":"applied","salary":90000,"remote":true}`)

	// The caller's context is not the bound. An engine that never answers is
	// the materialiser's own deadline, or this call does not return.
	if _, err := mat.Materialise(t.Context(), applications()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Materialise through an encoder that never answers: %v, want context.DeadlineExceeded", err)
	}
}
