package duckparquet_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/duckparquet"
	"github.com/tunedev/nerve/internal/core/projection"
)

// binaryVar is where an operator says which DuckDB to run, and what a test
// reads to find one. Without it the suite looks on PATH, and without that it
// skips: this is the engine tests, and they need the engine the same way the
// object-store tests need a store.
const binaryVar = "NERVE_DUCKDB_BIN"

// schema is the table every case below writes: one column of each type a
// manifest can declare, so a round trip proves the whole vocabulary survives
// Parquet and not just the easy half of it.
var schema = projection.Schema{
	{Name: "id", Type: projection.String},
	{Name: "size", Type: projection.Int},
	{Name: "score", Type: projection.Float},
	{Name: "open", Type: projection.Bool},
	{Name: "seen", Type: projection.Timestamp},
}

// duckdb is the engine under test, or a skip.
func duckdb(t *testing.T) string {
	t.Helper()

	binary := os.Getenv(binaryVar)
	if binary == "" {
		found, err := exec.LookPath("duckdb")
		if err != nil {
			t.Skipf("no duckdb: set %s to one, or put it on PATH", binaryVar)
		}
		binary = found
	}
	return binary
}

// instant is a time a row carries and a test asserts came back.
func instant(t *testing.T, text string) time.Time {
	t.Helper()

	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return at.UTC()
}

// query runs one statement over data as a Parquet file and returns what DuckDB
// printed, which is how a test reads a table this package wrote without this
// package reading it back.
func query(t *testing.T, binary string, data []byte, statement string) string {
	t.Helper()

	dir := t.TempDir()
	at := filepath.Join(dir, "table.parquet")
	if err := os.WriteFile(at, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The session zone is fixed, so a timestamp reads back as the instant it
	// was written rather than as whatever zone the machine running the test is
	// in.
	command := exec.CommandContext(t.Context(), binary, "-no-init", "-bail", "-batch", "-noheader", "-list", "-c",
		"SET TimeZone='UTC'; "+strings.ReplaceAll(statement, "TABLE", "'"+at+"'"))
	var said bytes.Buffer
	command.Stdout = &said
	command.Stderr = &said
	if err := command.Run(); err != nil {
		t.Fatalf("query %q: %v: %s", statement, err, said.String())
	}
	return strings.TrimSpace(said.String())
}

func TestEncodeWritesEveryTypeTheVocabularyHas(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	rows := []projection.Row{
		{"acme", int64(9), 0.5, true, instant(t, "2026-01-02T03:04:05Z")},
		{"bolt", int64(-1), 2.25, false, instant(t, "2019-12-31T23:59:59Z")},
	}

	data, err := duckparquet.New(binary).Encode(t.Context(), schema, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if got, want := query(t, binary, data, "SELECT * FROM TABLE ORDER BY id"), strings.Join([]string{
		"acme|9|0.5|true|2026-01-02 03:04:05+00",
		"bolt|-1|2.25|false|2019-12-31 23:59:59+00",
	}, "\n"); got != want {
		t.Fatalf("the table reads back as\n%s\nwant\n%s", got, want)
	}
}

func TestEncodeWritesTheDeclaredTypesAndNotGuessedOnes(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	// Every value here would be inferred as something narrower or wider than
	// the manifest declared: a whole number as an integer of some width, a
	// score that happens to be whole as an integer, a timestamp as text.
	rows := []projection.Row{{"acme", int64(1), 2.0, true, instant(t, "2026-01-02T03:04:05Z")}}

	data, err := duckparquet.New(binary).Encode(t.Context(), schema, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	want := strings.Join([]string{
		"id|VARCHAR", "size|BIGINT", "score|DOUBLE", "open|BOOLEAN", "seen|TIMESTAMP WITH TIME ZONE",
	}, "\n")
	if got := query(t, binary, data, "SELECT column_name, column_type FROM (DESCRIBE SELECT * FROM TABLE)"); got != want {
		t.Fatalf("the table describes as\n%s\nwant\n%s", got, want)
	}
}

func TestEncodeWritesACellTheRecordHadNothingIn(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	rows := []projection.Row{{"acme", nil, nil, nil, nil}}

	data, err := duckparquet.New(binary).Encode(t.Context(), schema, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, want := query(t, binary, data, "SELECT size IS NULL AND seen IS NULL FROM TABLE"), "true"; got != want {
		t.Fatalf("the null cells read back as %q, want %q", got, want)
	}
}

func TestEncodeKeepsTheSchemaOfATableWithNoRows(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	data, err := duckparquet.New(binary).Encode(t.Context(), schema, nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if got, want := query(t, binary, data, "SELECT count(*) FROM TABLE"), "0"; got != want {
		t.Fatalf("the empty table holds %s rows, want %s", got, want)
	}
	if got, want := query(t, binary, data, "SELECT count(*) FROM (DESCRIBE SELECT * FROM TABLE)"), "5"; got != want {
		t.Fatalf("the empty table has %s columns, want %s", got, want)
	}
}

func TestEncodeIsTheSameBytesOnEveryRun(t *testing.T) {
	t.Parallel()

	// E10.4 asks a re-run to be a no-op producing the same result. It is a
	// ticket of its own, and it starts from whether the engine is repeatable
	// at all — a writer stamping a time or an id into the file would make
	// every convergence check downstream fail on the engine rather than on
	// anything the projection did.
	binary := duckdb(t)
	rows := []projection.Row{
		{"acme", int64(9), 0.5, true, instant(t, "2026-01-02T03:04:05Z")},
		{"bolt", int64(-1), 2.25, false, instant(t, "2019-12-31T23:59:59Z")},
	}

	encoder := duckparquet.New(binary)
	first, err := encoder.Encode(t.Context(), schema, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for range 3 {
		again, err := encoder.Encode(t.Context(), schema, rows)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("two encodes of one table wrote %d and %d bytes that differ", len(first), len(again))
		}
	}
}

func TestEncodeRefusesATableWithNoColumns(t *testing.T) {
	t.Parallel()

	// Refused before anything is run, so a build with no engine gets the same
	// answer as one with an engine.
	if _, err := duckparquet.New("duckdb").Encode(t.Context(), nil, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Encode of a table with no columns: %v, want fs.ErrInvalid", err)
	}
}

func TestEncodeRefusesARowThatIsNotTheSchema(t *testing.T) {
	t.Parallel()

	rows := []projection.Row{{"acme"}}
	if _, err := duckparquet.New("duckdb").Encode(t.Context(), schema, rows); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Encode of a row shorter than the schema: %v, want fs.ErrInvalid", err)
	}
}

func TestEncodeReportsWhatTheEngineSaid(t *testing.T) {
	t.Parallel()

	rows := []projection.Row{{"acme", int64(9), 0.5, true, instant(t, "2026-01-02T03:04:05Z")}}
	_, err := duckparquet.New("/nonexistent/duckdb").Encode(t.Context(), schema, rows)
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/duckdb") {
		t.Fatalf("Encode through an engine that is not there: %v, want the binary named", err)
	}
}

func TestEncodeStopsAtTheCallersDeadline(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	rows := []projection.Row{{"acme", int64(9), 0.5, true, instant(t, "2026-01-02T03:04:05Z")}}
	if _, err := duckparquet.New(binary).Encode(ctx, schema, rows); err == nil {
		t.Fatal("Encode on a cancelled context returned no error")
	}
}

func TestEncodeLeavesNothingBehind(t *testing.T) {
	binary := duckdb(t)
	// TMPDIR is where the encoder puts its conversation, so pointing it at an
	// empty directory and finding it empty afterwards is the whole check.
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	rows := []projection.Row{{"acme", int64(9), 0.5, true, instant(t, "2026-01-02T03:04:05Z")}}
	if _, err := duckparquet.New(binary).Encode(t.Context(), schema, rows); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("the encoder left %d entries in its temporary directory, want none", len(left))
	}
}

func TestEncodeWritesAColumnNamedLikeSQL(t *testing.T) {
	t.Parallel()

	// A manifest names its columns and nothing checks them against a keyword
	// list, so a projection with a column called "order" has to materialise.
	// It does because the names go out as string literals and the statement
	// never spells one: read_json is told the columns and COPY selects them
	// all.
	binary := duckdb(t)
	awkward := projection.Schema{
		{Name: "order", Type: projection.String},
		{Name: "at", Type: projection.Timestamp},
	}
	rows := []projection.Row{{"second", instant(t, "2026-01-02T03:04:05Z")}}

	data, err := duckparquet.New(binary).Encode(t.Context(), awkward, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, want := query(t, binary, data, `SELECT "order", "at" FROM TABLE`), "second|2026-01-02 03:04:05+00"; got != want {
		t.Fatalf("the table reads back as %q, want %q", got, want)
	}
}

func TestEncodeWritesAColumnNameThatCouldEndTheStatement(t *testing.T) {
	t.Parallel()

	// The statement is built by this package and the names in it come from a
	// manifest somebody wrote, so a name carrying a quote has to stay a name.
	binary := duckdb(t)
	awkward := projection.Schema{{Name: "it's", Type: projection.String}}
	rows := []projection.Row{{"held"}}

	data, err := duckparquet.New(binary).Encode(t.Context(), awkward, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, want := query(t, binary, data, `SELECT "it's" FROM TABLE`), "held"; got != want {
		t.Fatalf("the table reads back as %q, want %q", got, want)
	}
}
