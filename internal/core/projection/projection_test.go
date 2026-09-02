package projection_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/projection"
)

// uploads is the projection of what a workspace has uploaded, declared over the
// shape the resource store already writes: one directory per resource, holding
// the meta.json that says what it is. Nothing in the projection package knows
// this shape, which is the point — it is a manifest, not a type.
func uploads() projection.Manifest {
	return projection.Manifest{
		Name:   "uploads",
		Source: projection.Source{Root: "/resources", File: "meta.json"},
		Columns: []projection.Column{
			{Name: "resource", Type: projection.String, From: projection.FromName},
			{Name: "content_type", Type: projection.String, From: projection.FromField, Field: "content_type"},
			{Name: "size", Type: projection.Int, From: projection.FromField, Field: "size"},
			{Name: "complete", Type: projection.Bool, From: projection.FromField, Field: "complete"},
		},
		SliceBy: "content_type",
		Rollup: projection.Rollup{
			GroupBy: []string{"content_type"},
			Measures: []projection.Measure{
				{Name: "resources", Op: projection.Count},
				{Name: "bytes", Op: projection.Sum, Column: "size"},
				{Name: "largest", Op: projection.Max, Column: "size"},
			},
		},
	}
}

// The acceptance: the manifest declares the projection, and both tables a
// projection has come back derived from it. No schema is written anywhere —
// here, or in whatever materialises one.
func TestManifestDeclaresBothSchemas(t *testing.T) {
	m := uploads()
	if err := m.Valid(); err != nil {
		t.Fatalf("Valid: %v", err)
	}

	wantSlice := projection.Schema{
		{Name: "resource", Type: projection.String},
		{Name: "content_type", Type: projection.String},
		{Name: "size", Type: projection.Int},
		{Name: "complete", Type: projection.Bool},
	}
	if got := m.SliceSchema(); !slices.Equal(got, wantSlice) {
		t.Fatalf("SliceSchema = %v, want %v", got, wantSlice)
	}

	wantRollup := projection.Schema{
		{Name: "content_type", Type: projection.String},
		{Name: "resources", Type: projection.Int},
		{Name: "bytes", Type: projection.Int},
		{Name: "largest", Type: projection.Int},
	}
	if got := m.RollupSchema(); !slices.Equal(got, wantRollup) {
		t.Fatalf("RollupSchema = %v, want %v", got, wantRollup)
	}
}

// A column added to the manifest is a column in both tables, and the package
// changes not at all. This is the difference between a declared projection and
// a schema somebody maintains beside it.
func TestAColumnAddedToTheManifestReachesBothSchemas(t *testing.T) {
	m := uploads()
	m.Columns = append(m.Columns, projection.Column{Name: "uploaded", Type: projection.Timestamp, From: projection.FromField, Field: "uploaded"})
	m.Rollup.GroupBy = []string{"content_type", "complete"}
	m.Rollup.Measures = append(m.Rollup.Measures, projection.Measure{Name: "newest", Op: projection.Max, Column: "uploaded"})

	if err := m.Valid(); err != nil {
		t.Fatalf("Valid: %v", err)
	}
	if got, want := m.SliceSchema()[4], (projection.Field{Name: "uploaded", Type: projection.Timestamp}); got != want {
		t.Fatalf("last slice field = %v, want %v", got, want)
	}

	wantRollup := projection.Schema{
		{Name: "content_type", Type: projection.String},
		{Name: "complete", Type: projection.Bool},
		{Name: "resources", Type: projection.Int},
		{Name: "bytes", Type: projection.Int},
		{Name: "largest", Type: projection.Int},
		{Name: "newest", Type: projection.Timestamp},
	}
	if got := m.RollupSchema(); !slices.Equal(got, wantRollup) {
		t.Fatalf("RollupSchema = %v, want %v", got, wantRollup)
	}
}

// A rollup that declares neither a grouping nor a measure is the union of the
// slices, so the combined table has the slice's columns.
func TestRollupDeclaringNothingIsTheUnionOfTheSlices(t *testing.T) {
	m := uploads()
	m.Rollup = projection.Rollup{}

	if err := m.Valid(); err != nil {
		t.Fatalf("Valid: %v", err)
	}
	if got, want := m.RollupSchema(), m.SliceSchema(); !slices.Equal(got, want) {
		t.Fatalf("RollupSchema = %v, want the slice schema %v", got, want)
	}
}

// Grouping with no measures is a table of the distinct values, and measuring
// with no grouping is one row over everything. Both are rollups, so neither
// falls back to the union.
func TestGroupingAndMeasuringAreIndependent(t *testing.T) {
	grouped := uploads()
	grouped.Rollup = projection.Rollup{GroupBy: []string{"content_type"}}
	want := projection.Schema{{Name: "content_type", Type: projection.String}}
	if got := grouped.RollupSchema(); !slices.Equal(got, want) {
		t.Fatalf("grouped RollupSchema = %v, want %v", got, want)
	}

	total := uploads()
	total.Rollup = projection.Rollup{Measures: []projection.Measure{{Name: "bytes", Op: projection.Sum, Column: "size"}}}
	want = projection.Schema{{Name: "bytes", Type: projection.Int}}
	if got := total.RollupSchema(); !slices.Equal(got, want) {
		t.Fatalf("total RollupSchema = %v, want %v", got, want)
	}
}

// A count is a number of rows whatever it counted; every other measure keeps
// the type it reduced, because the sum of two sizes is a size.
func TestMeasureTypesFollowTheColumnTheyReduce(t *testing.T) {
	m := uploads()
	m.Columns = append(m.Columns, projection.Column{Name: "ratio", Type: projection.Float, From: projection.FromField, Field: "ratio"})
	m.Rollup = projection.Rollup{Measures: []projection.Measure{
		{Name: "rows", Op: projection.Count},
		{Name: "ratio_total", Op: projection.Sum, Column: "ratio"},
		{Name: "first", Op: projection.Min, Column: "resource"},
	}}

	if err := m.Valid(); err != nil {
		t.Fatalf("Valid: %v", err)
	}
	want := projection.Schema{
		{Name: "rows", Type: projection.Int},
		{Name: "ratio_total", Type: projection.Float},
		{Name: "first", Type: projection.String},
	}
	if got := m.RollupSchema(); !slices.Equal(got, want) {
		t.Fatalf("RollupSchema = %v, want %v", got, want)
	}
}

// What a manifest may not declare. Each of these produces a table that cannot
// be materialised, or one whose columns cannot be told apart.
func TestManifestsThatDeclareNothingMaterialisable(t *testing.T) {
	cases := []struct {
		name string
		want error
		make func(projection.Manifest) projection.Manifest
	}{
		{"no name", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Name = ""
			return m
		}},
		{"name with a separator", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Name = "reports/uploads"
			return m
		}},
		{"relative source root", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Source.Root = "resources"
			return m
		}},
		{"unclean source root", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Source.Root = "/resources/"
			return m
		}},
		{"record file with a separator", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Source.File = "meta/data.json"
			return m
		}},
		{"no columns", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Columns = nil
			return m
		}},
		{"unknown column type", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Columns[1].Type = "decimal"
			return m
		}},
		{"field column naming no field", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Columns[1].Field = ""
			return m
		}},
		{"name column naming a field", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Columns[0].Field = "resource"
			return m
		}},
		{"column from nowhere", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Columns[1].From = "path"
			return m
		}},
		{"two columns of one name", fs.ErrExist, func(m projection.Manifest) projection.Manifest {
			m.Columns[2].Name = "content_type"
			return m
		}},
		{"slicing by nothing", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.SliceBy = ""
			return m
		}},
		{"slicing by an undeclared column", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.SliceBy = "uploaded"
			return m
		}},
		{"slicing by a float", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Columns[2].Type = projection.Float
			m.SliceBy = "size"
			return m
		}},
		{"grouping by an undeclared column", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.GroupBy = []string{"uploaded"}
			return m
		}},
		{"grouping by one column twice", fs.ErrExist, func(m projection.Manifest) projection.Manifest {
			m.Rollup.GroupBy = []string{"content_type", "content_type"}
			return m
		}},
		{"measure with no name", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[0].Name = ""
			return m
		}},
		{"count naming a column", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[0].Column = "size"
			return m
		}},
		{"reducing an undeclared column", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[1].Column = "uploaded"
			return m
		}},
		{"summing a string", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[1].Column = "content_type"
			return m
		}},
		{"taking the maximum of a boolean", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[2].Column = "complete"
			return m
		}},
		{"unknown operation", fs.ErrInvalid, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[1].Op = "median"
			return m
		}},
		{"measure named after a grouped column", fs.ErrExist, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[0].Name = "content_type"
			return m
		}},
		{"two measures of one name", fs.ErrExist, func(m projection.Manifest) projection.Manifest {
			m.Rollup.Measures[2].Name = "bytes"
			return m
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.make(uploads()).Valid(); !errors.Is(err, c.want) {
				t.Fatalf("Valid = %v, want %v", err, c.want)
			}
		})
	}
}
