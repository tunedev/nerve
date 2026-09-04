package projection_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/projection"
)

// everyType is a manifest with one column of each type the vocabulary has, so a
// record exercises the whole of what a row can hold.
func everyType() projection.Manifest {
	return projection.Manifest{
		Name:   "records",
		Source: projection.Source{Root: source, File: "meta.json"},
		Columns: []projection.Column{
			{Name: "id", Type: projection.String, From: projection.FromName},
			{Name: "title", Type: projection.String, From: projection.FromField, Field: "title"},
			{Name: "size", Type: projection.Int, From: projection.FromField, Field: "size"},
			{Name: "score", Type: projection.Float, From: projection.FromField, Field: "score"},
			{Name: "open", Type: projection.Bool, From: projection.FromField, Field: "open"},
			{Name: "at", Type: projection.Timestamp, From: projection.FromField, Field: "at"},
		},
		SliceBy: "id",
		Rollup:  projection.Rollup{},
	}
}

// materialiseOne writes one record and returns the row the manifest read out of
// it, which is the only way a row is reached from outside the package.
func materialiseOne(t *testing.T, manifest projection.Manifest, name, doc string) projection.Row {
	t.Helper()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, name, doc)
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	if len(encoder.tables) != 1 || len(encoder.tables[0]) != 1 {
		t.Fatalf("encoder saw %d tables, want one holding one row", len(encoder.tables))
	}
	return encoder.tables[0][0]
}

func TestRowReadsEveryTypeTheVocabularyHas(t *testing.T) {
	t.Parallel()

	row := materialiseOne(t, everyType(), "acme",
		`{"title":"engineer","size":9,"score":0.5,"open":true,"at":"2026-01-02T03:04:05Z"}`)

	want := []projection.Value{"acme", "engineer", int64(9), 0.5, true, mustTime(t, "2026-01-02T03:04:05Z")}
	if !slices.Equal([]projection.Value(row), want) {
		t.Fatalf("row is %v, want %v", row, want)
	}
}

func TestRowReadsATimestampInUTC(t *testing.T) {
	t.Parallel()

	// The same instant written with an offset reads as the same value, so two
	// records written in different zones fall in one slice.
	row := materialiseOne(t, everyType(), "acme", `{"at":"2026-01-02T04:04:05+01:00"}`)
	if got, want := row[5], projection.Value(mustTime(t, "2026-01-02T03:04:05Z")); got != want {
		t.Fatalf("timestamp is %v, want %v", got, want)
	}
}

func TestRowHoldsNothingForAFieldTheRecordLacks(t *testing.T) {
	t.Parallel()

	// A record saying less than its neighbours is still a record, so every
	// column it did not answer is null rather than a refusal.
	row := materialiseOne(t, everyType(), "acme", `{"title":"engineer","size":null}`)
	for i, value := range row[2:] {
		if value != nil {
			t.Errorf("column %d holds %v, want nothing", i+2, value)
		}
	}
}

func TestRowRefusesAFieldTheColumnCannotHold(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"a number in a string column":      `{"title":9}`,
		"a string in an int column":        `{"size":"9"}`,
		"a boolean in a float column":      `{"score":true}`,
		"a number in a boolean column":     `{"open":1}`,
		"a number in a timestamp column":   `{"at":17}`,
		"an object in a string column":     `{"title":{"held":"engineer"}}`,
		"a list in a string column":        `{"title":["engineer"]}`,
		"a fraction in an int column":      `{"size":1.5}`,
		"a date that is not RFC 3339":      `{"at":"2026-01-02"}`,
		"a document that is not an object": `["engineer"]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mat, _, fsys := materialiser(t)
			writeRecord(t, fsys, "acme", doc)
			if _, err := mat.Materialise(t.Context(), everyType()); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("Materialise of %s: %v, want fs.ErrInvalid", name, err)
			}
		})
	}
}

func TestRowRefusesADocumentThatIsNotJSON(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", "not a document")
	if _, err := mat.Materialise(t.Context(), everyType()); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Materialise of bytes that are not a document: %v, want fs.ErrInvalid", err)
	}
}

func TestRowKeepsAnIntegerAFloatCannotSpell(t *testing.T) {
	t.Parallel()

	// 2^53+1 is the first integer a float64 rounds, so a decoder that went
	// through one would read this back as the even number below it.
	row := materialiseOne(t, everyType(), "acme", `{"size":9007199254740993}`)
	if got, want := row[2], projection.Value(int64(9007199254740993)); got != want {
		t.Fatalf("int column holds %v, want %v", got, want)
	}
}
