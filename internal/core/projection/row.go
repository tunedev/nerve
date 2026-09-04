package projection

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"time"
)

// Value is one cell of a row. It is one of nil, string, int64, float64, bool or
// time.Time, which is the manifest's type vocabulary and nothing else.
//
// nil is a cell the record had nothing to put in, and is what a column reading
// an absent field holds. It is a value rather than a refusal because a table
// whose rows had to be complete would drop a record for saying less than its
// neighbours, and what a projection is asked for is every record.
type Value any

// Row is one record's cells, in the order SliceSchema declares them.
type Row []Value

// Record is one directory the manifest's source names: what it is called, and
// what its document says.
type Record struct {
	// Name is the directory the record occupies, which is the only place its
	// identity is written.
	Name string
	// Doc is the record's file, decoded. A column reading a field reads it
	// here.
	Doc map[string]any
}

// row is r read through m's columns, one cell per column, in declared order.
//
// A field the document does not have is nil; a field it has in the wrong shape
// is fs.ErrInvalid naming the column, because a manifest that declared a column
// an int is a promise about the table and not a preference.
func (m Manifest) row(r Record) (Row, error) {
	row := make(Row, len(m.Columns))
	for i, column := range m.Columns {
		value, err := column.read(r)
		if err != nil {
			return nil, fmt.Errorf("projection %q: record %q: %w", m.Name, r.Name, err)
		}
		row[i] = value
	}
	return row, nil
}

// read is what c holds for r.
func (c Column) read(r Record) (Value, error) {
	if c.From == FromName {
		return r.Name, nil
	}
	raw, present := r.Doc[c.Field]
	if !present || raw == nil {
		return nil, nil
	}
	return c.coerce(raw)
}

// coerce is raw as c's declared type, refusing what that type cannot hold.
//
// JSON has one number, so an int column takes a float64 that is whole and
// refuses one that is not: silently truncating 1.5 to 1 would write a table
// nobody could tell had lost anything.
func (c Column) coerce(raw any) (Value, error) {
	switch c.Type {
	case String:
		text, ok := raw.(string)
		if !ok {
			return nil, c.mismatch(raw)
		}
		return text, nil
	case Int:
		number, ok := raw.(json.Number)
		if !ok {
			return nil, c.mismatch(raw)
		}
		whole, err := number.Int64()
		if err != nil {
			return nil, fmt.Errorf("column %q is an int and the field holds %s: %w", c.Name, number, fs.ErrInvalid)
		}
		return whole, nil
	case Float:
		number, ok := raw.(json.Number)
		if !ok {
			return nil, c.mismatch(raw)
		}
		size, err := number.Float64()
		if err != nil {
			return nil, fmt.Errorf("column %q is a float and the field holds %s: %w", c.Name, number, fs.ErrInvalid)
		}
		return size, nil
	case Bool:
		yes, ok := raw.(bool)
		if !ok {
			return nil, c.mismatch(raw)
		}
		return yes, nil
	case Timestamp:
		text, ok := raw.(string)
		if !ok {
			return nil, c.mismatch(raw)
		}
		at, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return nil, fmt.Errorf("column %q is a timestamp and the field holds %q, which is not RFC 3339: %w", c.Name, text, fs.ErrInvalid)
		}
		return at.UTC(), nil
	}
	return nil, c.mismatch(raw)
}

// mismatch is what a column says about a field it cannot hold. It names the
// shape that arrived rather than the Go type, so the message reads as something
// about the document.
func (c Column) mismatch(raw any) error {
	return fmt.Errorf("column %q is a %s and field %q holds %s: %w", c.Name, c.Type, c.Field, shape(raw), fs.ErrInvalid)
}

// shape is what a decoded JSON value is, in the vocabulary of the document it
// came out of.
func shape(raw any) string {
	switch raw.(type) {
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	return "nothing this reads"
}
