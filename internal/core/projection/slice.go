package projection

import (
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Slice is the rows sharing one value of the manifest's slice column: the value
// as it names a file, and the rows that carried it.
//
// The key is part of the name because that is what lets a slice be written once
// and never rewritten. A slice whose key could not name a file would have to be
// addressed some other way, and the manifest refuses a float column for exactly
// that reason.
type Slice struct {
	Key  string
	Rows []Row
}

// slices are m's rows cut into slices, sorted by key, each keeping its rows in
// the order they were read.
//
// The order is fixed twice over because E10.4 asks a re-run to produce the same
// result: slices come back sorted rather than in the order keys were first
// seen, and rows keep the order the tree listed their records in.
func (m Manifest) slices(rows []Row) ([]Slice, error) {
	at := m.sliceColumn()
	byKey := make(map[string][]Row, len(rows))
	for _, row := range rows {
		key, err := m.sliceKey(row[at])
		if err != nil {
			return nil, err
		}
		byKey[key] = append(byKey[key], row)
	}

	cut := make([]Slice, 0, len(byKey))
	for key, rows := range byKey {
		cut = append(cut, Slice{Key: key, Rows: rows})
	}
	slices.SortFunc(cut, func(a, b Slice) int { return strings.Compare(a.Key, b.Key) })
	return cut, nil
}

// sliceColumn is where the column the slices are cut by sits in a row. A
// validated manifest declares it, so there is always one.
func (m Manifest) sliceColumn() int {
	for i, column := range m.Columns {
		if column.Name == m.SliceBy {
			return i
		}
	}
	return 0
}

// sliceKey is what value is called as a slice, refusing what cannot name one.
//
// A record with nothing in the slice column is refused rather than gathered
// into a slice of its own: the value is the slice's name, and a name invented
// here would be a value the projection never read.
func (m Manifest) sliceKey(value Value) (string, error) {
	key, err := name(value)
	if err != nil {
		return "", fmt.Errorf("projection %q slices by %q: %w", m.Name, m.SliceBy, err)
	}
	switch {
	case key == "":
		return "", fmt.Errorf("projection %q slices by %q, which is empty in a record: %w", m.Name, m.SliceBy, fs.ErrInvalid)
	case strings.Contains(key, "/"):
		return "", fmt.Errorf("projection %q slices by %q, which is %q in a record and contains a separator: %w", m.Name, m.SliceBy, key, fs.ErrInvalid)
	case key == "." || key == "..":
		return "", fmt.Errorf("projection %q slices by %q, which is %q in a record and names a directory: %w", m.Name, m.SliceBy, key, fs.ErrInvalid)
	}
	return key, nil
}

// name is a value spelled the one way a slice is named by it. A timestamp is
// spelled in UTC, which is how row read it, so two records written in different
// zones at the same instant fall in one slice.
func name(value Value) (string, error) {
	switch held := value.(type) {
	case string:
		return held, nil
	case int64:
		return strconv.FormatInt(held, 10), nil
	case bool:
		return strconv.FormatBool(held), nil
	case time.Time:
		return held.UTC().Format(time.RFC3339Nano), nil
	}
	return "", fmt.Errorf("a record has nothing in it: %w", fs.ErrInvalid)
}
