package projection_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/projection"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// scribe is an SQL writer that answers with whatever a test set and remembers
// the question and the table it was shown, so a core test asserts what a model
// would have been told without a model anywhere near it.
type scribe struct {
	sql      string
	fail     error
	question string
	table    llm.Table
}

func (s *scribe) WriteSQL(_ context.Context, question string, table llm.Table) (string, error) {
	s.question, s.table = question, table
	return s.sql, s.fail
}

// engine is a querier that answers every statement with one fixed row and
// remembers what it was asked, so a test can tell a statement that reached it
// from one that was refused.
type engine struct {
	columns   projection.Schema
	rows      []projection.Row
	fail      error
	statement string
	table     []byte
	name      string
	calls     int
}

func (*engine) Extension() string { return "table" }

func (e *engine) Query(_ context.Context, name string, table []byte, statement string) (projection.Schema, []projection.Row, error) {
	e.calls++
	e.name, e.table, e.statement = name, table, statement
	return e.columns, e.rows, e.fail
}

// asker is an asker over a tree already holding the rollup of applications(),
// with the writer and the engine it reaches them through.
func asker(t *testing.T, sql string) (*projection.Asker, *scribe, *engine) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount("/", memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := table.Mkdir(t.Context(), target); err != nil {
		t.Fatalf("Mkdir(%q): %v", target, err)
	}
	if err := table.Write(t.Context(), target+"/applications.table", []byte("the rollup")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	writer := &scribe{sql: sql}
	querier := &engine{
		columns: projection.Schema{{Name: "stage", Type: projection.String}},
		rows:    []projection.Row{{"offer"}},
	}
	ask, err := projection.NewAsker(table, writer, querier, target, time.Minute)
	if err != nil {
		t.Fatalf("NewAsker: %v", err)
	}
	return ask, writer, querier
}

// The whole path: a question in somebody's own words reaches the writer, the
// statement it answers with reaches the engine along with the rollup the
// manifest names, and the rows come back with the statement that produced them.
func TestAQuestionIsAnsweredByRunningTheStatementTheWriterWrote(t *testing.T) {
	t.Parallel()

	statement := "SELECT stage FROM applications WHERE salary > 100000"
	ask, writer, querier := asker(t, statement)

	answer, err := ask.Ask(t.Context(), applications(), "which stages pay over 100k")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if writer.question != "which stages pay over 100k" {
		t.Errorf("the writer was asked %q, want the caller's own question", writer.question)
	}
	if querier.statement != statement {
		t.Errorf("the engine ran %q, want %q", querier.statement, statement)
	}
	if querier.name != "applications" || string(querier.table) != "the rollup" {
		t.Errorf("the engine was given table %q holding %q, want the rollup of applications", querier.name, querier.table)
	}
	if answer.SQL != statement {
		t.Errorf("Answer.SQL = %q, want the statement that was run", answer.SQL)
	}
	if len(answer.Rows) != 1 || answer.Rows[0][0] != "offer" {
		t.Errorf("Answer.Rows = %v, want the engine's one row", answer.Rows)
	}
	if want := (projection.Schema{{Name: "stage", Type: projection.String}}); !slices.Equal(answer.Columns, want) {
		t.Errorf("Answer.Columns = %v, want %v", answer.Columns, want)
	}
}

// The writer is shown the rollup's columns and the projection's name, which is
// the table the statement will actually be run against. Showing it the slice
// schema would invite a statement naming columns the rollup does not have.
func TestTheWriterIsShownTheRollupAndNotTheSliceTable(t *testing.T) {
	t.Parallel()

	manifest := applications()
	manifest.Rollup = projection.Rollup{
		GroupBy:  []string{"stage"},
		Measures: []projection.Measure{{Name: "total", Op: projection.Sum, Column: "salary"}},
	}
	ask, writer, _ := asker(t, "SELECT * FROM applications")

	if _, err := ask.Ask(t.Context(), manifest, "how much is on the table"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	want := llm.Table{Name: "applications", Columns: []llm.Column{
		{Name: "stage", Type: "string"},
		{Name: "total", Type: "int"},
	}}
	if writer.table.Name != want.Name || !slices.Equal(writer.table.Columns, want.Columns) {
		t.Errorf("the writer was shown %+v, want %+v", writer.table, want)
	}
}

// A statement that is not one read-only SELECT never reaches the engine. The
// model is writing code against data it has not seen, so the check is what
// makes the worst it can do a refusal.
func TestAStatementThatIsNotOneReadOnlySelectIsRefused(t *testing.T) {
	t.Parallel()

	refused := map[string]string{
		"a second statement":      "SELECT stage FROM applications; DROP TABLE applications",
		"a write":                 "DELETE FROM applications",
		"a write behind a select": "SELECT 1; DELETE FROM applications",
		"the local filesystem":    "SELECT * FROM read_csv('/etc/passwd')",
		"another database":        "SELECT * FROM applications UNION ALL SELECT * FROM attach('other.db')",
		"an extension":            "SELECT stage FROM applications WHERE install = 1",
		"nothing at all":          "   ",
		"prose":                   "I would use SELECT stage FROM applications",
	}

	for what, statement := range refused {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			ask, _, querier := asker(t, statement)
			_, err := ask.Ask(t.Context(), applications(), "a question")
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Ask = %v, want fs.ErrInvalid", err)
			}
			if querier.calls != 0 {
				t.Errorf("the engine saw %d calls, want 0: the statement was refused", querier.calls)
			}
		})
	}
}

// A value that happens to spell a refused word is a value and not a name. A
// stage called "load" is a legal thing to ask about.
func TestARefusedWordInsideALiteralIsAValue(t *testing.T) {
	t.Parallel()

	ask, _, querier := asker(t, "SELECT stage FROM applications WHERE stage = 'load'")
	if _, err := ask.Ask(t.Context(), applications(), "what is on load"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if querier.calls != 1 {
		t.Errorf("the engine saw %d calls, want 1", querier.calls)
	}
}

// A trailing semicolon is how everybody writes SQL, and it is the one thing
// separating a statement from two of them, so it comes off rather than being
// refused.
func TestATrailingSemicolonIsRemovedRatherThanRefused(t *testing.T) {
	t.Parallel()

	ask, _, querier := asker(t, "SELECT stage FROM applications;\n")
	answer, err := ask.Ask(t.Context(), applications(), "which stages")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if want := "SELECT stage FROM applications"; answer.SQL != want || querier.statement != want {
		t.Errorf("ran %q and answered %q, want %q", querier.statement, answer.SQL, want)
	}
}

// A question asked of a projection that was never materialised is the missing
// file, not an empty answer: a caller told there are no rows would read it as
// there being nothing to find.
func TestAQuestionAboutAProjectionThatWasNeverMaterialisedIsNotFound(t *testing.T) {
	t.Parallel()

	ask, _, _ := asker(t, "SELECT 1")
	manifest := applications()
	manifest.Name = "interviews"

	if _, err := ask.Ask(t.Context(), manifest, "how many"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Ask = %v, want fs.ErrNotExist", err)
	}
}

// An empty question is refused before the model is paid for it.
func TestAnEmptyQuestionIsRefused(t *testing.T) {
	t.Parallel()

	ask, writer, _ := asker(t, "SELECT 1")
	if _, err := ask.Ask(t.Context(), applications(), " \t"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Ask = %v, want fs.ErrInvalid", err)
	}
	if writer.question != "" {
		t.Errorf("the writer was asked %q, want no call at all", writer.question)
	}
}

// A composition missing a piece is refused at the composition root rather than
// panicking on the first question.
func TestAnAskerWithNothingToWriteSQLWithIsRefused(t *testing.T) {
	t.Parallel()

	table := vfs.NewMountTable()
	if err := table.Mount("/", memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if _, err := projection.NewAsker(table, nil, &engine{}, target, time.Minute); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewAsker with no writer = %v, want fs.ErrInvalid", err)
	}
	if _, err := projection.NewAsker(table, &scribe{}, nil, target, time.Minute); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewAsker with no querier = %v, want fs.ErrInvalid", err)
	}
	if _, err := projection.NewAsker(table, &scribe{}, &engine{}, target, 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewAsker with no timeout = %v, want fs.ErrInvalid", err)
	}
}
