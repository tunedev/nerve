package projection_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/projection"
)

// rolledUp is the applications manifest combining its slices as rollup says.
func rolledUp(rollup projection.Rollup) projection.Manifest {
	manifest := applications()
	manifest.Rollup = rollup
	return manifest
}

func TestRollupIsWrittenBesideTheSlices(t *testing.T) {
	t.Parallel()

	mat, _, fsys := materialiser(t)
	// A record whose stage names a slice after the projection itself, which is
	// what a rollup written inside the slice directory would overwrite.
	writeRecord(t, fsys, "acme", `{"stage":"applications","salary":90000,"remote":true}`)

	got, err := mat.Materialise(t.Context(), applications())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	if want := "/projections/applications.table"; got.Rollup != want {
		t.Fatalf("Materialise rolled up into %q, want %q", got.Rollup, want)
	}
	if want := []string{"/projections/applications/applications.table"}; !slices.Equal(got.Slices, want) {
		t.Fatalf("Materialise wrote %v, want %v", got.Slices, want)
	}
	for _, at := range append(got.Slices, got.Rollup) {
		if _, err := fsys.Stat(t.Context(), at); err != nil {
			t.Errorf("Stat(%q): %v", at, err)
		}
	}
}

func TestRollupDeclaringNothingCarriesEveryRow(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)
	writeRecord(t, fsys, "cog", `{"stage":"applied","salary":85000,"remote":true}`)

	if _, err := mat.Materialise(t.Context(), applications()); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	// The slices end to end, in the order they were cut and read.
	assertTable(t, encoder.rollup(), [][]projection.Value{
		{"acme", "applied", int64(90000), true},
		{"cog", "applied", int64(85000), true},
		{"bolt", "offer", int64(120000), false},
	})
}

func TestRollupGroupsAndMeasures(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)
	writeRecord(t, fsys, "cog", `{"stage":"applied","salary":85000,"remote":true}`)

	manifest := rolledUp(projection.Rollup{
		GroupBy: []string{"stage"},
		Measures: []projection.Measure{
			{Name: "roles", Op: projection.Count},
			{Name: "payroll", Op: projection.Sum, Column: "salary"},
			{Name: "lowest", Op: projection.Min, Column: "salary"},
			{Name: "highest", Op: projection.Max, Column: "salary"},
		},
	})
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	assertTable(t, encoder.rollup(), [][]projection.Value{
		{"applied", int64(2), int64(175000), int64(85000), int64(90000)},
		{"offer", int64(1), int64(120000), int64(120000), int64(120000)},
	})
	if want := manifest.RollupSchema(); !slices.Equal(encoder.schemas[len(encoder.schemas)-1], want) {
		t.Fatalf("rollup schema %v, want %v", encoder.schemas[len(encoder.schemas)-1], want)
	}
}

func TestRollupGroupingByNothingMeasuresEveryRow(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":90000,"remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)

	manifest := rolledUp(projection.Rollup{Measures: []projection.Measure{
		{Name: "roles", Op: projection.Count},
		{Name: "payroll", Op: projection.Sum, Column: "salary"},
	}})
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	assertTable(t, encoder.rollup(), [][]projection.Value{{int64(2), int64(210000)}})
}

func TestRollupMeasuresNothingWhenNoRecordFilledTheColumn(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","remote":true}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":120000,"remote":false}`)

	manifest := rolledUp(projection.Rollup{
		GroupBy: []string{"stage"},
		Measures: []projection.Measure{
			{Name: "roles", Op: projection.Count},
			{Name: "payroll", Op: projection.Sum, Column: "salary"},
			{Name: "highest", Op: projection.Max, Column: "salary"},
		},
	})
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	// The rows are still counted, because a record that said nothing about a
	// salary is still a record; the salary is nothing rather than zero.
	assertTable(t, encoder.rollup(), [][]projection.Value{
		{"applied", int64(1), nil, nil},
		{"offer", int64(1), int64(120000), int64(120000)},
	})
}

func TestRollupTellsAnEmptyValueFromNoValue(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "a-record", `{"title":""}`)
	writeRecord(t, fsys, "b-record", `{}`)
	writeRecord(t, fsys, "c-record", `{"title":""}`)

	manifest := everyType()
	manifest.Rollup = projection.Rollup{
		GroupBy:  []string{"title"},
		Measures: []projection.Measure{{Name: "records", Op: projection.Count}},
	}
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	// A record that said nothing and a record that said the empty string did
	// not say the same thing, and the one that said nothing comes first.
	assertTable(t, encoder.rollup(), [][]projection.Value{
		{nil, int64(1)},
		{"", int64(2)},
	})
}

func TestRollupGroupsInTheOrderTheValuesHaveNotTheOrderTheySpell(t *testing.T) {
	t.Parallel()

	mat, encoder, fsys := materialiser(t)
	writeRecord(t, fsys, "acme", `{"stage":"applied","salary":10}`)
	writeRecord(t, fsys, "bolt", `{"stage":"offer","salary":2}`)
	writeRecord(t, fsys, "cog", `{"stage":"screen"}`)

	manifest := rolledUp(projection.Rollup{
		GroupBy:  []string{"salary"},
		Measures: []projection.Measure{{Name: "roles", Op: projection.Count}},
	})
	if _, err := mat.Materialise(t.Context(), manifest); err != nil {
		t.Fatalf("Materialise: %v", err)
	}

	// 2 before 10, which is the order the numbers have and not the order they
	// are spelled in, and the salary nobody stated before both.
	assertTable(t, encoder.rollup(), [][]projection.Value{
		{nil, int64(1)},
		{int64(2), int64(1)},
		{int64(10), int64(1)},
	})
}

func TestRollupIsTheSameOnEveryRun(t *testing.T) {
	t.Parallel()

	manifest := rolledUp(projection.Rollup{
		GroupBy:  []string{"remote", "stage"},
		Measures: []projection.Measure{{Name: "payroll", Op: projection.Sum, Column: "salary"}},
	})

	// Ten runs over five groups. A rollup whose order came from a map would
	// disagree with itself well inside that.
	var first []projection.Row
	for range 10 {
		mat, encoder, fsys := materialiser(t)
		for i, stage := range []string{"applied", "screen", "offer", "rejected", "hired"} {
			writeRecord(t, fsys, stage+"-record", fmt.Sprintf(`{"stage":%q,"salary":%d,"remote":true}`, stage, i))
		}
		if _, err := mat.Materialise(t.Context(), manifest); err != nil {
			t.Fatalf("Materialise: %v", err)
		}
		if first == nil {
			first = encoder.rollup()
			continue
		}
		if got := encoder.rollup(); !sameTable(got, first) {
			t.Fatalf("rollup is %v, want %v on every run", got, first)
		}
	}
}

// sameTable reports whether two tables hold the same rows in the same order.
func sameTable(a, b []projection.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i, row := range a {
		if !slices.Equal([]projection.Value(row), []projection.Value(b[i])) {
			return false
		}
	}
	return true
}
