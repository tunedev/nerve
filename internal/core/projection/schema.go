package projection

// Field is one column of a materialised table: its name and what it holds.
type Field struct {
	Name string
	Type Type
}

// Schema is the columns of a table, in order. Nothing writes one: both schemas
// a projection has are returned by the two methods below, from the manifest
// alone, which is what makes a new column an edit to a manifest rather than a
// change to whatever materialises it.
type Schema []Field

// SliceSchema is the table one slice holds: the manifest's columns, in the
// order they were declared.
//
// The slice column is in it. A slice's rows all share that value, so a reader
// could infer it from the slice's name, but a table that only makes sense
// beside its own filename is not a table — and the rollup groups by ordinary
// columns without knowing which of them cut the slices.
func (m Manifest) SliceSchema() Schema {
	schema := make(Schema, len(m.Columns))
	for i, column := range m.Columns {
		schema[i] = Field{Name: column.Name, Type: column.Type}
	}
	return schema
}

// RollupSchema is the table the slices combine into: the grouped columns in the
// order they are grouped by, then the measures in the order they are declared.
//
// A rollup that declares neither is the union of the slices, so its schema is
// the slice schema.
//
// The order is the manifest's, never a map's, because a re-run is asked to
// produce the same result: a schema whose column order came from iterating a
// map would make the same manifest materialise differently on two runs.
func (m Manifest) RollupSchema() Schema {
	if len(m.Rollup.GroupBy) == 0 && len(m.Rollup.Measures) == 0 {
		return m.SliceSchema()
	}

	schema := make(Schema, 0, len(m.Rollup.GroupBy)+len(m.Rollup.Measures))
	for _, name := range m.Rollup.GroupBy {
		column, _ := m.column(name)
		schema = append(schema, Field{Name: column.Name, Type: column.Type})
	}
	for _, measure := range m.Rollup.Measures {
		schema = append(schema, Field{Name: measure.Name, Type: measure.resultType(m)})
	}
	return schema
}
