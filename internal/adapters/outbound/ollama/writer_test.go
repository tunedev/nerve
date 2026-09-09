package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/stability"
)

// generated is what the generation endpoint was sent, recorded so a test checks
// the wire rather than a mock's call count.
type generated struct {
	Model   string `json:"model"`
	System  string `json:"system"`
	Prompt  string `json:"prompt"`
	Stream  bool   `json:"stream"`
	Options struct {
		Temperature float64 `json:"temperature"`
	} `json:"options"`
}

// applications is the table every question below is asked over.
var applications = llm.Table{Name: "applications", Columns: []llm.Column{
	{Name: "stage", Type: "string"},
	{Name: "total", Type: "int"},
}}

// writing stands an Ollama in front of the writer, answering every call with
// response and recording what it was asked.
func writing(t *testing.T, response string, seen *generated) *ollama.Writer {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("the adapter posted to %q, want /api/generate", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(seen); err != nil {
			t.Errorf("decoding the request = %v", err)
		}
		writeJSON(t, w, map[string]any{"response": response, "done": true})
	}))
	t.Cleanup(server.Close)

	writer, err := ollama.NewWriter(server.URL, "qwen2.5-coder", server.Client())
	if err != nil {
		t.Fatalf("NewWriter(%q) = %v", server.URL, err)
	}
	t.Cleanup(writer.CloseIdleConnections)
	return writer
}

// The model is told what the table holds and what is being asked, and is asked
// for one answer at temperature zero: the same question over the same table has
// to produce the same statement, or nobody can check the number it returned.
func TestTheQuestionAndTheTableBothReachTheModel(t *testing.T) {
	var seen generated
	writer := writing(t, "SELECT stage FROM applications", &seen)

	statement, err := writer.WriteSQL(context.Background(), "which stages are there", applications)
	if err != nil {
		t.Fatalf("WriteSQL = %v", err)
	}
	if statement != "SELECT stage FROM applications" {
		t.Errorf("WriteSQL = %q, want the statement the endpoint answered", statement)
	}

	if seen.Model != "qwen2.5-coder" {
		t.Errorf("the endpoint was asked for %q, want the configured model", seen.Model)
	}
	if seen.Stream {
		t.Error("the endpoint was asked to stream, want one answer")
	}
	if seen.Options.Temperature != 0 {
		t.Errorf("temperature = %v, want 0: the same question has to answer the same way", seen.Options.Temperature)
	}
	if !strings.Contains(seen.System, "SELECT") {
		t.Errorf("the system prompt is %q, want the shape of the answer", seen.System)
	}
	for _, want := range []string{"applications", "stage", "total", "which stages are there"} {
		if !strings.Contains(seen.Prompt, want) {
			t.Errorf("the prompt does not carry %q:\n%s", want, seen.Prompt)
		}
	}
}

// A model fences code however plainly it is asked not to. The fence comes off,
// because a caller that refused the statement for not beginning with SELECT
// would be refusing a statement that was fine.
func TestAFencedStatementComesBackUnfenced(t *testing.T) {
	var seen generated
	writer := writing(t, "```sql\nSELECT stage FROM applications\n```", &seen)

	statement, err := writer.WriteSQL(context.Background(), "which stages", applications)
	if err != nil {
		t.Fatalf("WriteSQL = %v", err)
	}
	if statement != "SELECT stage FROM applications" {
		t.Errorf("WriteSQL = %q, want the statement without its fence", statement)
	}
}

// Prose is handed back as prose. Nothing here judges the statement, so a model
// that explained itself instead of answering is something the caller sees and
// refuses rather than something this quietly patches up.
func TestAModelThatExplainedItselfIsHandedBackAsItAnswered(t *testing.T) {
	var seen generated
	writer := writing(t, "I would use SELECT stage FROM applications", &seen)

	statement, err := writer.WriteSQL(context.Background(), "which stages", applications)
	if err != nil {
		t.Fatalf("WriteSQL = %v", err)
	}
	if statement != "I would use SELECT stage FROM applications" {
		t.Errorf("WriteSQL = %q, want what the endpoint said", statement)
	}
}

// An endpoint that answered nothing at all is a failure and not an empty
// statement, which would reach the caller as a refusal naming the wrong thing.
func TestAnEndpointThatAnsweredNothingIsAFailure(t *testing.T) {
	var seen generated
	writer := writing(t, "   \n", &seen)

	if _, err := writer.WriteSQL(context.Background(), "which stages", applications); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("WriteSQL = %v, want %v", err, fs.ErrInvalid)
	}
}

// A question with nothing in it, and a table with no columns to answer from,
// are both refused before the endpoint is paid for the round trip.
func TestThereIsNoCallWithoutAQuestionAndATable(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	writer, err := ollama.NewWriter(server.URL, "qwen2.5-coder", server.Client())
	if err != nil {
		t.Fatalf("NewWriter = %v", err)
	}
	defer writer.CloseIdleConnections()

	if _, err := writer.WriteSQL(context.Background(), " ", applications); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("WriteSQL with no question = %v, want %v", err, fs.ErrInvalid)
	}
	if _, err := writer.WriteSQL(context.Background(), "which stages", llm.Table{Name: "applications"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("WriteSQL over a table with no columns = %v, want %v", err, fs.ErrInvalid)
	}
	if called {
		t.Error("the endpoint was called, want no round trip at all")
	}
}

// A busy endpoint is transient here for the same reason it is transient for the
// embedder: the classification is the package's and not the verb's, so the
// chain passes a wobbling endpoint over whichever capability was asked for.
func TestABusyEndpointIsTransientForTheWriterToo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	writer, err := ollama.NewWriter(server.URL, "qwen2.5-coder", server.Client())
	if err != nil {
		t.Fatalf("NewWriter = %v", err)
	}
	defer writer.CloseIdleConnections()

	if _, err := writer.WriteSQL(context.Background(), "which stages", applications); !errors.Is(err, stability.ErrTransient) {
		t.Errorf("WriteSQL against a busy endpoint = %v, want %v", err, stability.ErrTransient)
	}
}
