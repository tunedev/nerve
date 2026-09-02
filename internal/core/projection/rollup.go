package projection

import (
	"fmt"
	"io/fs"
)

// Op is what a measure does to the column it reduces.
type Op string

const (
	// Count is how many rows fell in the group. It reduces no column.
	Count Op = "count"
	// Sum adds the column up, so the column has to be a number.
	Sum Op = "sum"
	// Min is the smallest value the column took in the group.
	Min Op = "min"
	// Max is the largest value the column took in the group.
	Max Op = "max"
)

// Measure is one reduced column of the rollup table: what it is called in the
// result, what it does, and which slice column it does it to.
//
// Column is empty for a count, because counting rows asks nothing of any
// column. It is required for the rest, because there is nothing to add up or
// take the smallest of otherwise.
type Measure struct {
	Name   string `json:"name"`
	Op     Op     `json:"op"`
	Column string `json:"column,omitempty"`
}

// Rollup is what the slices of a projection combine into: the columns rows are
// grouped by, and the measures computed inside each group.
//
// A rollup that declares neither is the union of the slices, and its schema is
// the slice schema. That is the honest default rather than a special case: a
// projection whose slices are already the table people want still has to say
// what the combined table is, and saying nothing says exactly that.
type Rollup struct {
	GroupBy  []string  `json:"group_by,omitempty"`
	Measures []Measure `json:"measures,omitempty"`
}

// valid returns fs.ErrInvalid for a rollup that cannot be computed from m's
// columns. Every name it uses has to be a column m declares, and no two output
// columns may share a name — a rollup with two "total" columns is a table whose
// second one cannot be read.
func (r Rollup) valid(m Manifest) error {
	out := make(map[string]struct{}, len(r.GroupBy)+len(r.Measures))

	for _, name := range r.GroupBy {
		if _, declared := m.column(name); !declared {
			return fmt.Errorf("projection %q groups by %q, which is not a column it declares: %w", m.Name, name, fs.ErrInvalid)
		}
		if _, taken := out[name]; taken {
			return fmt.Errorf("projection %q groups by %q twice: %w", m.Name, name, fs.ErrExist)
		}
		out[name] = struct{}{}
	}

	for _, measure := range r.Measures {
		if err := measure.valid(m); err != nil {
			return err
		}
		if _, taken := out[measure.Name]; taken {
			return fmt.Errorf("projection %q rolls up two columns named %q: %w", m.Name, measure.Name, fs.ErrExist)
		}
		out[measure.Name] = struct{}{}
	}
	return nil
}

// valid returns fs.ErrInvalid for a measure m's columns cannot answer.
func (measure Measure) valid(m Manifest) error {
	if measure.Name == "" {
		return fmt.Errorf("projection %q has a measure with no name: %w", m.Name, fs.ErrInvalid)
	}
	if measure.Op == Count {
		if measure.Column != "" {
			return fmt.Errorf("projection %q counts rows into %q and also names column %q: %w", m.Name, measure.Name, measure.Column, fs.ErrInvalid)
		}
		return nil
	}

	column, declared := m.column(measure.Column)
	if !declared {
		return fmt.Errorf("projection %q measures %q with %q, which is not a column it declares: %w", m.Name, measure.Name, measure.Column, fs.ErrInvalid)
	}
	switch measure.Op {
	case Sum:
		if !column.Type.numeric() {
			return fmt.Errorf("projection %q sums %q, which is a %s: %w", m.Name, measure.Column, column.Type, fs.ErrInvalid)
		}
	case Min, Max:
		if !column.Type.ordered() {
			return fmt.Errorf("projection %q takes the %s of %q, which is a %s: %w", m.Name, measure.Op, measure.Column, column.Type, fs.ErrInvalid)
		}
	default:
		return fmt.Errorf("projection %q measures %q with %q, which is not one of count, sum, min, max: %w", m.Name, measure.Name, measure.Op, fs.ErrInvalid)
	}
	return nil
}

// resultType is what the measure's column holds, given the slice column it
// reduces. A count is a number of rows whatever it counted; everything else
// keeps the type it reduced, because the sum of two sizes is a size.
func (measure Measure) resultType(m Manifest) Type {
	if measure.Op == Count {
		return Int
	}
	column, _ := m.column(measure.Column)
	return column.Type
}
