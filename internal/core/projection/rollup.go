package projection

import (
	"cmp"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"
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

// combine is the table the slices roll up into: the rows the manifest's
// grouping and measures reduce them to, in an order two runs agree on.
//
// A rollup declaring neither a grouping nor a measure is the union of the
// slices, which keeps the order the slices were cut in and the rows were read
// in — the same order the slice files were written in.
func (m Manifest) combine(cut []Slice) []Row {
	rows := union(cut)
	if len(m.Rollup.GroupBy) == 0 && len(m.Rollup.Measures) == 0 {
		return rows
	}
	return m.reduce(rows)
}

// union is every slice's rows, end to end.
func union(cut []Slice) []Row {
	rows := make([]Row, 0, len(cut))
	for _, slice := range cut {
		rows = append(rows, slice.Rows...)
	}
	return rows
}

// group is the rows that agreed on every grouped column: what they agreed on,
// and the key that says they agreed.
type group struct {
	key   string
	cells []Value
	rows  []Row
}

// reduce is one row per group of rows: the grouped cells, then each measure
// over the rows that fell in the group.
//
// A rollup that groups by nothing and measures something is one group over
// every row, which is what a table of totals with no breakdown is.
func (m Manifest) reduce(rows []Row) []Row {
	at := make([]int, len(m.Rollup.GroupBy))
	for i, name := range m.Rollup.GroupBy {
		at[i] = m.columnAt(name)
	}

	groups := make(map[string]*group, len(rows))
	for _, row := range rows {
		cells := make([]Value, len(at))
		for i, column := range at {
			cells[i] = row[column]
		}
		id := key(cells)
		held, seen := groups[id]
		if !seen {
			held = &group{key: id, cells: cells, rows: nil}
			groups[id] = held
		}
		held.rows = append(held.rows, row)
	}

	ordered := make([]*group, 0, len(groups))
	for _, held := range groups {
		ordered = append(ordered, held)
	}
	slices.SortFunc(ordered, (*group).before)

	out := make([]Row, len(ordered))
	for i, held := range ordered {
		row := make(Row, 0, len(held.cells)+len(m.Rollup.Measures))
		row = append(row, held.cells...)
		for _, measure := range m.Rollup.Measures {
			row = append(row, m.measured(measure, held.rows))
		}
		out[i] = row
	}
	return out
}

// before orders two groups by the values they grouped on, falling back to their
// keys.
//
// The fallback is what makes the order total: two cells a comparison calls
// equal and a key spells differently would otherwise come back in whatever
// order the map was ranged in, and E10.4 asks a re-run to produce the same
// table.
func (g *group) before(other *group) int {
	for i, cell := range g.cells {
		if order := compare(cell, other.cells[i]); order != 0 {
			return order
		}
	}
	return strings.Compare(g.key, other.key)
}

// measured is what one measure reduces a group's rows to.
func (m Manifest) measured(measure Measure, rows []Row) Value {
	if measure.Op == Count {
		return int64(len(rows))
	}
	at := m.columnAt(measure.Column)
	column, _ := m.column(measure.Column)
	switch measure.Op {
	case Sum:
		if column.Type == Float {
			return total[float64](rows, at)
		}
		return total[int64](rows, at)
	case Min:
		return extreme(rows, at, -1)
	case Max:
		return extreme(rows, at, 1)
	}
	return nil
}

// total adds a column up across rows, passing over the cells no record filled.
//
// A group whose every cell was empty totals to nothing rather than to zero: a
// zero here would be a sum somebody measured, and nothing was measured.
func total[N int64 | float64](rows []Row, at int) Value {
	var sum N
	measured := false
	for _, row := range rows {
		held, filled := row[at].(N)
		if !filled {
			continue
		}
		sum += held
		measured = true
	}
	if !measured {
		return nil
	}
	return sum
}

// extreme is the smallest or largest value a column took across rows, want
// being -1 for the smallest and 1 for the largest. Cells no record filled are
// passed over, so a group that filled none has neither.
func extreme(rows []Row, at, want int) Value {
	var found Value
	for _, row := range rows {
		cell := row[at]
		if cell == nil {
			continue
		}
		if found == nil || compare(cell, found) == want {
			found = cell
		}
	}
	return found
}

// key is cells spelled as one string no other cells spell, so a map groups by
// them.
//
// Every part carries its length, because "ab" beside "c" and "a" beside "bc"
// are the same characters and not the same group, and a cell no record filled
// is marked rather than spelled — a missing value and an empty string are two
// different things to have grouped on.
func key(cells []Value) string {
	var spelled strings.Builder
	for _, cell := range cells {
		if cell == nil {
			spelled.WriteString("-|")
			continue
		}
		text := spell(cell)
		fmt.Fprintf(&spelled, "%d:%s|", len(text), text)
	}
	return spelled.String()
}

// spell is a value written out, in the one spelling every value of its type
// has. A timestamp is spelled in UTC, so two records written in different zones
// at the same instant group together.
func spell(value Value) string {
	switch held := value.(type) {
	case string:
		return held
	case int64:
		return strconv.FormatInt(held, 10)
	case float64:
		return strconv.FormatFloat(held, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(held)
	case time.Time:
		return held.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

// compare orders two cells of one column, with a cell no record filled coming
// first. Both hold that column's declared type, which is what lets one switch
// order every column a rollup can group by.
func compare(a, b Value) int {
	if a == nil || b == nil {
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return -1
		default:
			return 1
		}
	}
	switch held := a.(type) {
	case string:
		other, _ := b.(string)
		return strings.Compare(held, other)
	case int64:
		other, _ := b.(int64)
		return cmp.Compare(held, other)
	case float64:
		other, _ := b.(float64)
		return cmp.Compare(held, other)
	case bool:
		other, _ := b.(bool)
		return cmp.Compare(boolOrder(held), boolOrder(other))
	case time.Time:
		other, _ := b.(time.Time)
		return held.Compare(other)
	}
	return 0
}

// boolOrder is where a boolean sits in an order, false before true.
func boolOrder(yes bool) int {
	if yes {
		return 1
	}
	return 0
}
