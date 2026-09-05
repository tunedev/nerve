package duckparquet_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/duckparquet"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/projection"
)

// fixed is an SQL writer that answers every question with one statement, which
// is what a model would have answered on a machine with a model on it. What is
// under test here is everything after the model.
type fixed string

func (f fixed) WriteSQL(context.Context, string, llm.Table) (string, error) { return string(f), nil }

// TestAQuestionIsAnsweredOverTheRollup is the ticket: a question, the statement
// a model wrote for it, and the rows DuckDB answers with off the one combined
// file the workspace rolled up into.
//
// It runs the real engine over the real asker because that seam is the whole
// delivery. The core tests prove the checking and the routing with an engine
// that runs nothing; this one proves the statement runs.
func TestAQuestionIsAnsweredOverTheRollup(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	table, manifest, _ := materialised(t, binary)

	statement := "SELECT stage, payroll FROM applications WHERE roles > 1"
	ask, err := projection.NewAsker(table, fixed(statement), duckparquet.NewQuerier(binary), projections, time.Minute)
	if err != nil {
		t.Fatalf("NewAsker: %v", err)
	}

	answer, err := ask.Ask(t.Context(), manifest, "which stage has more than one role on it, and what is it worth")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if answer.SQL != statement {
		t.Errorf("Answer.SQL = %q, want the statement that was run", answer.SQL)
	}
	want := projection.Schema{
		{Name: "stage", Type: projection.String},
		{Name: "payroll", Type: projection.Int},
	}
	if !slices.Equal(answer.Columns, want) {
		t.Fatalf("Answer.Columns = %v, want %v", answer.Columns, want)
	}
	// Two applications are at applied and their salaries add up to the one that
	// was stated, which is the rollup answering rather than the slices.
	if len(answer.Rows) != 1 || !slices.Equal(answer.Rows[0], projection.Row{"applied", int64(90000)}) {
		t.Errorf("Answer.Rows = %#v, want one row of applied and 90000", answer.Rows)
	}
}

// A statement that reads the machine the engine runs on never reaches the
// engine, whatever the model wrote and whatever it was asked.
func TestAStatementThatReachesOutsideTheRollupNeverRuns(t *testing.T) {
	t.Parallel()

	binary := duckdb(t)
	table, manifest, _ := materialised(t, binary)

	ask, err := projection.NewAsker(table, fixed("SELECT * FROM read_csv('/etc/passwd')"), duckparquet.NewQuerier(binary), projections, time.Minute)
	if err != nil {
		t.Fatalf("NewAsker: %v", err)
	}
	if _, err := ask.Ask(t.Context(), manifest, "who is on this machine"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Ask = %v, want fs.ErrInvalid", err)
	}
}
