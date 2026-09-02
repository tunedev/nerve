// Package projection declares a columnar view of a workspace's records, so that
// what a table looks like is a manifest rather than a schema somebody wrote out
// by hand.
//
// A record is a directory on the tree holding one JSON file, which is how a
// resource, a document and a curated edge are already stored: identity lives in
// the path and the rest lives in the file. A manifest names the area those
// directories sit under, the file that makes one of them a record, and the
// columns to read out of it. Every schema below that — the slice table, the
// rollup table — is derived from those columns, so adding a column is an edit
// to a manifest and no type in this repo changes.
//
// A projection materialises in two stages, and the manifest declares both. A
// slice is the rows sharing one value of the slice column, which is what makes
// a slice a file that can be written once and never rewritten: the value is
// part of its name. A rollup is what those slices combine into, one table per
// workspace, grouped and reduced as the manifest says. Nothing here reads or
// writes anything; a manifest is a value, and the engine that materialises it
// is an adapter that takes one.
package projection

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// Type is the column vocabulary a projection has. It is small on purpose: every
// type here is one a columnar format stores natively, so a manifest never asks
// for a column that has to be spelled as text and parsed back.
type Type string

const (
	String    Type = "string"
	Int       Type = "int"
	Float     Type = "float"
	Bool      Type = "bool"
	Timestamp Type = "timestamp"
)

// known reports whether t is a type this vocabulary has.
func (t Type) known() bool {
	switch t {
	case String, Int, Float, Bool, Timestamp:
		return true
	}
	return false
}

// numeric reports whether values of t can be added together, which is what
// separates a measure that can be summed from one that can only be counted.
func (t Type) numeric() bool { return t == Int || t == Float }

// ordered reports whether values of t have a smallest and a largest, which is
// what a minimum or a maximum needs and a boolean does not usefully have.
func (t Type) ordered() bool { return t == Int || t == Float || t == String || t == Timestamp }

// Origin says where one column's value is read from.
//
// There are two because a record's identity is not inside it. A resource is
// named by the directory it occupies and says nothing about that name in its
// meta.json, so a projection that could only read fields would produce a table
// whose rows cannot be told apart.
type Origin string

const (
	// FromField reads a named field of the record's document.
	FromField Origin = "field"
	// FromName reads the record's own name, the directory it occupies.
	FromName Origin = "name"
)

// Column is one column of the slice table: what it is called, what it holds,
// and where its value comes from.
type Column struct {
	Name  string `json:"name"`
	Type  Type   `json:"type"`
	From  Origin `json:"from"`
	Field string `json:"field,omitempty"`
}

// valid returns fs.ErrInvalid for a column no table can have.
func (c Column) valid() error {
	if c.Name == "" {
		return fmt.Errorf("column has no name: %w", fs.ErrInvalid)
	}
	if !c.Type.known() {
		return fmt.Errorf("column %q has type %q, which is not one of string, int, float, bool, timestamp: %w", c.Name, c.Type, fs.ErrInvalid)
	}
	switch c.From {
	case FromField:
		if c.Field == "" {
			return fmt.Errorf("column %q reads a field and names none: %w", c.Name, fs.ErrInvalid)
		}
	case FromName:
		if c.Field != "" {
			return fmt.Errorf("column %q reads the record name and also names field %q: %w", c.Name, c.Field, fs.ErrInvalid)
		}
	default:
		return fmt.Errorf("column %q comes from %q, which is neither a field nor the name: %w", c.Name, c.From, fs.ErrInvalid)
	}
	return nil
}

// Source is where the rows are: the area of the tree the record directories sit
// under, and the file that makes one of those directories a record.
//
// The two are separate for the reason a library's kind is separate from its
// root: the file says what a record is, the root says which mount it is kept
// on, and the same records move to another mount without the manifest learning
// anything new.
type Source struct {
	Root string `json:"root"`
	File string `json:"file"`
}

// valid returns fs.ErrInvalid for a source nothing can be read from. Root is a
// rooted clean path because it is an area of the tree, the mount table's rule
// for a prefix; File is one flat segment because a record is one directory deep
// and a file named "meta/data.json" describes a record that is two.
func (s Source) valid() error {
	if !strings.HasPrefix(s.Root, "/") || path.Clean(s.Root) != s.Root {
		return fmt.Errorf("source root %q is not a rooted clean path: %w", s.Root, fs.ErrInvalid)
	}
	switch {
	case s.File == "":
		return fmt.Errorf("source under %q names no record file: %w", s.Root, fs.ErrInvalid)
	case strings.Contains(s.File, "/"):
		return fmt.Errorf("source file %q contains a separator: %w", s.File, fs.ErrInvalid)
	case s.File == "." || s.File == "..":
		return fmt.Errorf("source file %q names a directory: %w", s.File, fs.ErrInvalid)
	}
	return nil
}

// Manifest is one projection: what it is called, where its rows come from, the
// columns of one row, which column cuts the rows into slices, and what those
// slices roll up into.
//
// Name is what the materialised tables are called, so it is a flat segment: a
// name carrying a separator writes a slice somewhere other than where the
// projection was asked to put it.
type Manifest struct {
	Name    string   `json:"name"`
	Source  Source   `json:"source"`
	Columns []Column `json:"columns"`
	SliceBy string   `json:"slice_by"`
	Rollup  Rollup   `json:"rollup,omitzero"`
}

// Valid returns fs.ErrInvalid for a manifest nothing can be materialised from.
// It is the whole check: a manifest that passes here declares a slice schema
// and a rollup schema that both derive without failing.
func (m Manifest) Valid() error {
	if err := validName(m.Name); err != nil {
		return err
	}
	if err := m.Source.valid(); err != nil {
		return fmt.Errorf("projection %q: %w", m.Name, err)
	}
	if len(m.Columns) == 0 {
		return fmt.Errorf("projection %q has no columns: %w", m.Name, fs.ErrInvalid)
	}

	seen := make(map[string]struct{}, len(m.Columns))
	for _, column := range m.Columns {
		if err := column.valid(); err != nil {
			return fmt.Errorf("projection %q: %w", m.Name, err)
		}
		if _, taken := seen[column.Name]; taken {
			return fmt.Errorf("projection %q declares column %q twice: %w", m.Name, column.Name, fs.ErrExist)
		}
		seen[column.Name] = struct{}{}
	}

	if err := m.validSliceBy(); err != nil {
		return err
	}
	return m.Rollup.valid(m)
}

// validSliceBy checks the column the slices are cut by. It has to be a declared
// column, and it cannot be a float: the value becomes part of a slice's name,
// and two numbers that are equal have more than one spelling.
func (m Manifest) validSliceBy() error {
	if m.SliceBy == "" {
		return fmt.Errorf("projection %q names no column to slice by: %w", m.Name, fs.ErrInvalid)
	}
	column, declared := m.column(m.SliceBy)
	if !declared {
		return fmt.Errorf("projection %q slices by %q, which is not a column it declares: %w", m.Name, m.SliceBy, fs.ErrInvalid)
	}
	if column.Type == Float {
		return fmt.Errorf("projection %q slices by %q, which is a float and cannot name a slice: %w", m.Name, m.SliceBy, fs.ErrInvalid)
	}
	return nil
}

// column returns the declared column of that name, reporting whether there is
// one.
func (m Manifest) column(name string) (Column, bool) {
	for _, column := range m.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return Column{}, false
}

// validName returns fs.ErrInvalid for a projection name that cannot stand as
// one path segment.
func validName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("projection has no name: %w", fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("projection name %q contains a separator: %w", name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("projection name %q names a directory: %w", name, fs.ErrInvalid)
	}
	return nil
}
