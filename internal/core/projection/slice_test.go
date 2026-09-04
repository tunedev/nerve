package projection_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/projection"
)

// slicedBy is the everyType manifest cut by one of its own columns, which is
// how a test asks what a slice of that type is called.
func slicedBy(column string) projection.Manifest {
	manifest := everyType()
	manifest.SliceBy = column
	return manifest
}

func TestSliceIsNamedByTheValueThatCutIt(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]struct {
		by   string
		doc  string
		file string
	}{
		"a string":    {by: "title", doc: `{"title":"engineer"}`, file: "engineer.table"},
		"an int":      {by: "size", doc: `{"size":-9}`, file: "-9.table"},
		"a boolean":   {by: "open", doc: `{"open":false}`, file: "false.table"},
		"a timestamp": {by: "at", doc: `{"at":"2026-01-02T04:04:05+01:00"}`, file: "2026-01-02T03:04:05Z.table"},
		"the name":    {by: "id", doc: `{}`, file: "acme.table"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mat, _, fsys := materialiser(t)
			writeRecord(t, fsys, "acme", want.doc)
			got, err := mat.Materialise(t.Context(), slicedBy(want.by))
			if err != nil {
				t.Fatalf("Materialise: %v", err)
			}
			if files := []string{"/projections/records/" + want.file}; !slices.Equal(got.Slices, files) {
				t.Fatalf("Materialise wrote %v, want %v", got.Slices, files)
			}
		})
	}
}

func TestSliceRefusesAValueThatCannotNameAFile(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"nothing at all":  `{}`,
		"an empty string": `{"title":""}`,
		"a separator":     `{"title":"open/applied"}`,
		"this directory":  `{"title":"."}`,
		"the one above":   `{"title":".."}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mat, _, fsys := materialiser(t)
			writeRecord(t, fsys, "acme", doc)
			if _, err := mat.Materialise(t.Context(), slicedBy("title")); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("Materialise of a slice value that is %s: %v, want fs.ErrInvalid", name, err)
			}
		})
	}
}

func TestSlicesComeBackSortedByKey(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	// Written in an order that is neither the tree's nor the key's, so a
	// materialiser ranging over a map would land on this in some run.
	writeRecord(t, fsys, "b-record", `{"title":"offer"}`)
	writeRecord(t, fsys, "a-record", `{"title":"screen"}`)
	writeRecord(t, fsys, "c-record", `{"title":"applied"}`)

	got, err := mat.Materialise(t.Context(), slicedBy("title"))
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	want := []string{
		"/projections/records/applied.table",
		"/projections/records/offer.table",
		"/projections/records/screen.table",
	}
	if !slices.Equal(got.Slices, want) {
		t.Fatalf("Materialise wrote %v, want %v", got.Slices, want)
	}
	// The rows follow the same order, so the nth table is the nth slice.
	assertTable(t, encoder.tables[0], [][]projection.Value{{"c-record", "applied", nil, nil, nil, nil}})
	assertTable(t, encoder.tables[2], [][]projection.Value{{"a-record", "screen", nil, nil, nil, nil}})
}

func TestSlicesAreTheSameOnEveryRun(t *testing.T) {
	t.Parallel()

	// Ten runs of one projection over five slices. A materialiser whose order
	// came from a map would disagree with itself well inside that.
	var first []string
	for range 10 {
		mat, _, fsys := materialiser(t)
		for _, stage := range []string{"applied", "screen", "offer", "rejected", "hired"} {
			writeRecord(t, fsys, stage+"-record", `{"title":"`+stage+`"}`)
		}
		got, err := mat.Materialise(t.Context(), slicedBy("title"))
		if err != nil {
			t.Fatalf("Materialise: %v", err)
		}
		if first == nil {
			first = got.Slices
			continue
		}
		if !slices.Equal(got.Slices, first) {
			t.Fatalf("Materialise wrote %v, want %v on every run", got.Slices, first)
		}
	}
}
