package duckparquet_test

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/duckparquet"
	"github.com/tunedev/nerve/internal/core/projection"
)

// asked encodes rows under schema and runs statement over the result, which is
// the round trip a question about a rollup actually makes.
func asked(t *testing.T, binary, statement string, rows []projection.Row) (projection.Schema, []projection.Row) {
	t.Helper()

	data, err := duckparquet.New(binary).Encode(t.Context(), schema, rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	columns, answered, err := duckparquet.NewQuerier(binary).Query(t.Context(), "applications", data, statement)
	if err != nil {
		t.Fatalf("Query(%q): %v", statement, err)
	}
	return columns, answered
}

// The table is reached by the name the caller gave it, and every type the
// vocabulary has survives the round trip out to Parquet and back.
func TestAStatementReadsTheTableBackUnderItsOwnName(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	rows := []projection.Row{
		{"acme", int64(9), 0.5, true, instant(t, "2026-01-02T03:04:05Z")},
		{"bolt", int64(-1), 2.25, false, instant(t, "2019-12-31T23:59:59Z")},
	}

	columns, answered := asked(t, binary, "SELECT * FROM applications ORDER BY id", rows)

	if !slices.Equal(columns, schema) {
		t.Fatalf("the answer describes %v, want %v", columns, schema)
	}
	if len(answered) != 2 {
		t.Fatalf("the answer has %d rows, want 2", len(answered))
	}
	for i, want := range rows {
		if !slices.Equal(answered[i], want) {
			t.Errorf("row %d is %#v, want %#v", i, answered[i], want)
		}
	}
}

// The aggregates a question about a rollup actually asks for come back in the
// manifest's own vocabulary, which they only do if the widths DuckDB answers
// with are read: a sum of integers is a HUGEINT and prints as text, an average
// is a double and prints as a number.
func TestAnAggregateComesBackInTheManifestsVocabulary(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	rows := []projection.Row{
		{"acme", int64(90000), 1.0, true, instant(t, "2026-01-02T03:04:05Z")},
		{"bolt", int64(120000), 2.0, false, instant(t, "2019-12-31T23:59:59Z")},
	}

	columns, answered := asked(t, binary,
		"SELECT sum(size) AS total, avg(size) AS mean, count(*) AS n, max(seen) AS last FROM applications", rows)

	want := projection.Schema{
		{Name: "total", Type: projection.Int},
		{Name: "mean", Type: projection.Float},
		{Name: "n", Type: projection.Int},
		{Name: "last", Type: projection.Timestamp},
	}
	if !slices.Equal(columns, want) {
		t.Fatalf("the answer describes %v, want %v", columns, want)
	}
	if got := (projection.Row{int64(210000), 105000.0, int64(2), instant(t, "2026-01-02T03:04:05Z")}); !slices.Equal(answered[0], got) {
		t.Errorf("the answer is %#v, want %#v", answered[0], got)
	}
}

// An instant is the instant it was written whatever zone the machine asking is
// in, because the session is put in UTC before anything is read.
func TestAnInstantIsAnsweredInUTCWhateverTheMachineIsSetTo(t *testing.T) {
	binary := duckdb(t)
	t.Setenv("TZ", "Asia/Kolkata")

	_, answered := asked(t, binary, "SELECT seen FROM applications",
		[]projection.Row{{"acme", int64(1), 1.0, true, instant(t, "2026-01-02T03:04:05Z")}})

	at, ok := answered[0][0].(time.Time)
	if !ok {
		t.Fatalf("the answer holds %#v, want a time", answered[0][0])
	}
	if !at.Equal(instant(t, "2026-01-02T03:04:05Z")) {
		t.Errorf("the answer is %s, want the instant that was written", at)
	}
}

// A cell nothing filled comes back as nothing rather than as a zero, which is
// the rule the rows went out under.
func TestACellWithNothingInItComesBackAsNothing(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	_, answered := asked(t, binary, "SELECT id, size FROM applications",
		[]projection.Row{{"acme", nil, 1.0, true, instant(t, "2026-01-02T03:04:05Z")}})

	if len(answered) != 1 || answered[0][0] != "acme" || answered[0][1] != nil {
		t.Errorf("the answer is %#v, want the name and an empty cell", answered[0])
	}
}

// A question nothing matches is a described table with no rows, not a failure:
// a caller asking which applications are at offer when none are has an answer.
func TestAQuestionNothingMatchesIsATableWithNoRows(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	columns, answered := asked(t, binary, "SELECT id FROM applications WHERE size > 100",
		[]projection.Row{{"acme", int64(1), 1.0, true, instant(t, "2026-01-02T03:04:05Z")}})

	if len(columns) != 1 || columns[0].Name != "id" {
		t.Errorf("the answer describes %v, want the one column that was asked for", columns)
	}
	if len(answered) != 0 {
		t.Errorf("the answer has %d rows, want none", len(answered))
	}
}

// A column of a type a manifest could not have declared is refused rather than
// handed back spelled as text, because a caller reading it would compare it
// with a string and mean something else.
func TestAColumnOutsideTheVocabularyIsRefused(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	data, err := duckparquet.New(binary).Encode(t.Context(), schema,
		[]projection.Row{{"acme", int64(1), 1.0, true, instant(t, "2026-01-02T03:04:05Z")}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	_, _, err = duckparquet.NewQuerier(binary).Query(t.Context(), "applications", data, "SELECT seen::DATE AS day FROM applications")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Query = %v, want fs.ErrInvalid", err)
	}
}

// A statement DuckDB will not run is what DuckDB said about it, named by the
// engine that said it, rather than a decode failure over an empty answer.
func TestAStatementTheEngineRefusesIsWhatTheEngineSaid(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	data, err := duckparquet.New(binary).Encode(t.Context(), schema,
		[]projection.Row{{"acme", int64(1), 1.0, true, instant(t, "2026-01-02T03:04:05Z")}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	_, _, err = duckparquet.NewQuerier(binary).Query(t.Context(), "applications", data, "SELECT nothing FROM applications")
	if err == nil {
		t.Fatal("Query answered a statement naming a column that is not there")
	}
	if !strings.Contains(err.Error(), "nothing") {
		t.Errorf("Query = %v, want what DuckDB said about the column", err)
	}
}

// An empty table is refused before the engine is started: a projection that was
// never materialised is a question with no answer, and an empty Parquet file is
// not one DuckDB can read.
func TestAnEmptyTableIsRefused(t *testing.T) {
	t.Parallel()

	_, _, err := duckparquet.NewQuerier("duckdb").Query(t.Context(), "applications", nil, "SELECT 1")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Query = %v, want fs.ErrInvalid", err)
	}
}
