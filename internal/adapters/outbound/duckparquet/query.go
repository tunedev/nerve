package duckparquet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/projection"
)

// tableFile is what the table being asked about is called inside the temporary
// directory, so the statement carries no absolute path.
const tableFile = "table.parquet"

// stamp is how DuckDB prints an instant once the session is in UTC: a date, a
// time that may carry a fraction, and an offset in whole hours.
const stamp = "2006-01-02 15:04:05.999999-07"

// Querier answers a statement about one Parquet table through the DuckDB at
// binary.
//
// It is a second type rather than a verb on Encoder because the two are two
// ports, and a build is entitled to write its tables with one engine and read
// them back with another. They share a binary setting and nothing else.
type Querier struct {
	binary string
}

// NewQuerier returns the querier running binary. Whether it is there is not
// checked here, for the reason the encoder does not check either.
func NewQuerier(binary string) Querier { return Querier{binary: binary} }

// Extension is what a file this querier reads is called.
func (Querier) Extension() string { return "parquet" }

// Query runs statement over table, which holds the Parquet of the table called
// name, and returns the columns it answered with and the rows under them.
//
// The table reaches the statement as a common table expression rather than as a
// path, so the statement names the projection and never a file. That is what
// makes a statement written against the projection's own name runnable, and it
// is why the caller only has to allow statements that begin with SELECT: one
// that opened with a CTE of its own could not be wrapped.
//
// The session is put in UTC first. An instant is printed in the session's zone,
// and a zone that is not a whole number of hours off — Kolkata, Kathmandu —
// prints an offset this does not read. One zone for every machine is the answer
// to that and to the two spellings one instant would otherwise have.
func (q Querier) Query(ctx context.Context, name string, table []byte, statement string) (projection.Schema, []projection.Row, error) {
	if len(table) == 0 {
		return nil, nil, fmt.Errorf("query %q: the table is empty: %w", name, fs.ErrInvalid)
	}

	dir, err := os.MkdirTemp("", "nerve-duckparquet-")
	if err != nil {
		return nil, nil, fmt.Errorf("query %q: %w", name, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := os.WriteFile(filepath.Join(dir, tableFile), table, 0o600); err != nil {
		return nil, nil, fmt.Errorf("query %q: %w", name, err)
	}

	asked := over(name, statement)
	said, err := run(ctx, q.binary, dir, ".mode json", "SET TimeZone='UTC';", "DESCRIBE "+asked+";", asked+";")
	if err != nil {
		return nil, nil, fmt.Errorf("query %q: %w", name, err)
	}
	return answer(said)
}

// over is statement with the table it asks about bound to its name.
func over(name, statement string) string {
	return fmt.Sprintf("WITH %s AS (SELECT * FROM read_parquet(%s)) %s", quoted(name), literal(tableFile), statement)
}

// answer is what DuckDB said as a schema and its rows.
//
// Two answers arrive on stdout in order — the described columns, then the rows —
// because the run asked two questions. The description is what gives the row
// objects an order and a type, neither of which a JSON object carries.
func answer(said []byte) (projection.Schema, []projection.Row, error) {
	reader := json.NewDecoder(bytes.NewReader(said))
	reader.UseNumber()

	var described []struct {
		Name string `json:"column_name"`
		Type string `json:"column_type"`
	}
	if err := reader.Decode(&described); err != nil {
		return nil, nil, fmt.Errorf("read the described columns: %v: %w", err, fs.ErrInvalid)
	}

	schema := make(projection.Schema, len(described))
	for i, column := range described {
		kind, err := projectionType(column.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("column %q: %w", column.Name, err)
		}
		schema[i] = projection.Field{Name: column.Name, Type: kind}
	}

	var objects []map[string]any
	if err := reader.Decode(&objects); err != nil {
		return nil, nil, fmt.Errorf("read the rows: %v: %w", err, fs.ErrInvalid)
	}

	rows := make([]projection.Row, len(objects))
	for i, object := range objects {
		row := make(projection.Row, len(schema))
		for j, field := range schema {
			value, err := cell(field.Type, object[described[j].Name])
			if err != nil {
				return nil, nil, fmt.Errorf("row %d, column %q: %w", i, field.Name, err)
			}
			row[j] = value
		}
		rows[i] = row
	}
	return schema, rows, nil
}

// projectionType is what a DuckDB type is called in the manifest's vocabulary.
//
// A type outside that vocabulary is refused rather than spelled as text. The
// five types are what a projection can hold, so a column a caller could not
// have declared is a column it cannot be handed: a date that arrived as a
// string would read like a timestamp and compare like neither.
func projectionType(duck string) (projection.Type, error) {
	if strings.HasPrefix(duck, "DECIMAL") {
		return projection.Float, nil
	}
	switch duck {
	case "VARCHAR":
		return projection.String, nil
	case "TINYINT", "SMALLINT", "INTEGER", "BIGINT", "HUGEINT":
		return projection.Int, nil
	case "FLOAT", "DOUBLE":
		return projection.Float, nil
	case "BOOLEAN":
		return projection.Bool, nil
	case "TIMESTAMP WITH TIME ZONE":
		return projection.Timestamp, nil
	}
	return "", fmt.Errorf("DuckDB answered with a %s, which a projection has no type for: %w", duck, fs.ErrInvalid)
}

// cell is one JSON value as the projection value of that type.
//
// A number arrives as a number or as text depending on how wide it is — DuckDB
// prints a HUGEINT and a DECIMAL as a string so that no precision is lost on
// the way through JSON — so both spellings are read.
func cell(kind projection.Type, raw any) (projection.Value, error) {
	if raw == nil {
		return nil, nil
	}
	switch kind {
	case projection.String:
		return text(raw)
	case projection.Int:
		spelled, err := text(raw)
		if err != nil {
			return nil, err
		}
		whole, err := strconv.ParseInt(spelled, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is not a whole number: %w", spelled, fs.ErrInvalid)
		}
		return whole, nil
	case projection.Float:
		spelled, err := text(raw)
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseFloat(spelled, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is not a number: %w", spelled, fs.ErrInvalid)
		}
		return size, nil
	case projection.Bool:
		yes, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("%v is not a boolean: %w", raw, fs.ErrInvalid)
		}
		return yes, nil
	case projection.Timestamp:
		spelled, err := text(raw)
		if err != nil {
			return nil, err
		}
		at, err := time.Parse(stamp, spelled)
		if err != nil {
			return nil, fmt.Errorf("%q is not an instant: %w", spelled, fs.ErrInvalid)
		}
		return at.UTC(), nil
	}
	return nil, fmt.Errorf("no type: %w", fs.ErrInvalid)
}

// text is a JSON scalar as the characters it was printed with, whether it
// arrived quoted or not.
func text(raw any) (string, error) {
	switch spelled := raw.(type) {
	case string:
		return spelled, nil
	case json.Number:
		return spelled.String(), nil
	}
	return "", fmt.Errorf("%v is neither text nor a number: %w", raw, fs.ErrInvalid)
}

// quoted is a name as a SQL identifier, with the one character that could end
// it early doubled.
func quoted(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
