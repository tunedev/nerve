// Package duckparquet writes a projection's table as Parquet by handing it to
// DuckDB, and answers a statement about one by handing it back.
//
// DuckDB runs as a process rather than as a library. The repo's build is
// CGO_ENABLED=0 and asserts that two clean checkouts produce identical
// binaries; the cgo driver would end both, and it carries a prebuilt engine per
// platform besides. Postgres, RabbitMQ and the object store are already
// processes this talks to, so an engine that is one too is the shape the repo
// already has — it is invoked rather than dialled, which is the only difference.
//
// The conversation is a temporary directory and a handful of statements. Rows
// go out as newline-delimited JSON, the schema becomes the column types
// read_json is given rather than ones it guessed, and COPY writes the Parquet
// that comes back as bytes. A question goes the other way: the table is written
// into the directory, the statement reads it under the projection's own name,
// and DESCRIBE beside it is what gives the answer its columns and their types.
// Nothing persists: the directory is removed and the database is in memory, so
// two calls cannot see each other.
package duckparquet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tunedev/nerve/internal/core/projection"
)

// The files the conversation happens through, named relative to the temporary
// directory so the statement carries no absolute path.
const (
	rowsFile = "rows.ndjson"
	outFile  = "out.parquet"
)

// Encoder writes tables as Parquet through the DuckDB at binary.
//
// binary is a path or a command name rather than a compiled-in constant,
// because which DuckDB a build runs is an operator's answer: a distribution's,
// one unpacked beside the binary, or one on PATH.
type Encoder struct {
	binary string
}

// New returns the encoder running binary. Whether it is there is not checked
// here — a process that exists at boot and not at the first encode would have
// passed the check anyway, and the failure reads the same either way.
func New(binary string) Encoder { return Encoder{binary: binary} }

// Extension is what a file this encoder writes is called.
func (Encoder) Extension() string { return "parquet" }

// Encode returns the Parquet holding rows under schema.
//
// A table with no rows is a file with no rows: the schema is the column types
// DuckDB was given, not ones it inferred from the values, so an empty slice
// still reads back as the table it is.
func (e Encoder) Encode(ctx context.Context, schema projection.Schema, rows []projection.Row) ([]byte, error) {
	if len(schema) == 0 {
		return nil, fmt.Errorf("encode parquet: the table has no columns: %w", fs.ErrInvalid)
	}

	dir, err := os.MkdirTemp("", "nerve-duckparquet-")
	if err != nil {
		return nil, fmt.Errorf("encode parquet: %w", err)
	}
	// The directory going is not what the encode is for, and a caller told the
	// table failed because a temporary directory outlived it would be told the
	// wrong thing.
	defer func() { _ = os.RemoveAll(dir) }()

	ndjson, err := encodeRows(schema, rows)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, rowsFile), ndjson, 0o600); err != nil {
		return nil, fmt.Errorf("encode parquet: %w", err)
	}
	if _, err := run(ctx, e.binary, dir, copyStatement(schema)); err != nil {
		return nil, fmt.Errorf("encode parquet: %w", err)
	}

	parquet, err := os.ReadFile(filepath.Join(dir, outFile))
	if err != nil {
		return nil, fmt.Errorf("encode parquet: %w", err)
	}
	return parquet, nil
}

// run executes the commands in dir with a DuckDB that reads no init file, keeps
// no database and stops at the first error, and returns what it said on stdout.
//
// The deadline is the caller's. A materialiser bounds an encode the way the
// model gateway bounds a provider call, so an engine that hangs is the caller's
// timeout rather than a second one invented here.
//
// stdout is kept apart from stderr because a caller reading the answer is
// parsing it: a warning printed beside the rows would be read as one of them.
// A failure names both, since which of the two carries the reason is DuckDB's
// business.
func run(ctx context.Context, binary, dir string, commands ...string) ([]byte, error) {
	args := []string{"-no-init", "-bail", "-batch"}
	for _, command := range commands {
		args = append(args, "-c", command)
	}

	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	var out, said bytes.Buffer
	command.Stdout = &out
	command.Stderr = &said
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", binary, err, strings.TrimSpace(said.String()+out.String()))
	}
	return out.Bytes(), nil
}

// encodeRows is rows as newline-delimited JSON, one object per row keyed by the
// schema's column names.
//
// A value goes out as the JSON of what it is: a timestamp as RFC 3339, an
// integer as a whole number, a cell the record had nothing in as null. That is
// what lets read_json be given types rather than left to infer them.
func encodeRows(schema projection.Schema, rows []projection.Row) ([]byte, error) {
	var out bytes.Buffer
	for _, row := range rows {
		if len(row) != len(schema) {
			return nil, fmt.Errorf("encode parquet: a row holds %d values and the schema has %d columns: %w", len(row), len(schema), fs.ErrInvalid)
		}
		cells := make(map[string]any, len(schema))
		for i, field := range schema {
			cells[field.Name] = row[i]
		}
		line, err := json.Marshal(cells)
		if err != nil {
			return nil, fmt.Errorf("encode parquet: %v: %w", err, fs.ErrInvalid)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// copyStatement is the one statement an encode runs: read the rows as the
// schema's types and write them out as Parquet.
func copyStatement(schema projection.Schema) string {
	return fmt.Sprintf(
		"COPY (SELECT * FROM read_json(%s, format='newline_delimited', columns=%s)) TO %s (FORMAT PARQUET);",
		literal(rowsFile), columns(schema), literal(outFile),
	)
}

// columns is the schema as the struct read_json takes: every column named, with
// the DuckDB type the manifest declared it as.
func columns(schema projection.Schema) string {
	var out strings.Builder
	out.WriteByte('{')
	for i, field := range schema {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(literal(field.Name))
		out.WriteByte(':')
		out.WriteString(literal(duckType(field.Type)))
	}
	out.WriteByte('}')
	return out.String()
}

// duckType is what a projection type is called in DuckDB. A timestamp keeps its
// zone, because a row read it as an instant and a column that dropped the zone
// would be an instant spelled as if it were a local time.
func duckType(t projection.Type) string {
	switch t {
	case projection.Int:
		return "BIGINT"
	case projection.Float:
		return "DOUBLE"
	case projection.Bool:
		return "BOOLEAN"
	case projection.Timestamp:
		return "TIMESTAMP WITH TIME ZONE"
	case projection.String:
		return "VARCHAR"
	}
	return "VARCHAR"
}

// literal is text as a SQL string, with the one character that could end it
// early doubled.
func literal(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "''") + "'"
}
