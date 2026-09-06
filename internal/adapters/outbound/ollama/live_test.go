package ollama_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/llmtest"
)

// endpointEnv turns this file on. Nothing here runs without an Ollama to talk
// to, which is what keeps the rest of the package's tests true on a plane: they
// assert the wire against a server in this process, and this asserts that a
// real model keeps the same promises.
//
// `make up` starts the endpoint these defaults point at. The model has to be
// pulled into it: `docker compose exec ollama ollama pull nomic-embed-text`.
const (
	endpointEnv = "NERVE_OLLAMA_ENDPOINT"
	modelEnv    = "NERVE_OLLAMA_MODEL"
	sqlModelEnv = "NERVE_OLLAMA_SQL_MODEL"
)

// live is the adapter over the real endpoint, or the reason there is none.
func live(t *testing.T) *ollama.Embedder {
	t.Helper()

	base := os.Getenv(endpointEnv)
	if base == "" {
		t.Skipf("set %s to run against an Ollama; `make up` starts one", endpointEnv)
	}

	model := os.Getenv(modelEnv)
	if model == "" {
		model = "nomic-embed-text"
	}

	embedder, err := ollama.New(base, model, nil)
	if err != nil {
		t.Fatalf("New(%q, %q) = %v", base, model, err)
	}
	t.Cleanup(embedder.CloseIdleConnections)
	return embedder
}

// A real model keeps the promises the port makes, held to by the suite every
// endpoint runs. The rest of this package asserts the wire against a server in
// this process; this asserts that a model on the other end of it answers the
// way llm.Embedder says an endpoint does.
func TestLiveConformance(t *testing.T) {
	llmtest.RunEmbedder(t, live(t))
}

// A model the endpoint has not pulled is a config answer and stops the chain,
// so a misspelled model name is loud rather than an index quietly built by
// whichever endpoint answered next.
func TestLiveAModelThatIsNotPulledIsAnAnswer(t *testing.T) {
	base := os.Getenv(endpointEnv)
	if base == "" {
		t.Skipf("set %s to run against an Ollama; `make up` starts one", endpointEnv)
	}

	embedder, err := ollama.New(base, "no-such-model-nerve-test", nil)
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	defer embedder.CloseIdleConnections()

	if _, err := embedder.Embed(context.Background(), []string{"the pumps"}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Embed with an unpulled model = %v, want %v", err, fs.ErrNotExist)
	}
}

// A real model answers a question about a table with a statement that runs
// against it: it begins with SELECT, and it names the table and a column it was
// shown. That is the whole of what the caller will accept, and asserting it
// here is what makes the refusal in the core a check rather than a wall.
func TestLiveWriteSQLAnswersAStatementOverTheTableItWasShown(t *testing.T) {
	base := os.Getenv(endpointEnv)
	if base == "" {
		t.Skipf("set %s to run against an Ollama; `make up` starts one", endpointEnv)
	}

	model := os.Getenv(sqlModelEnv)
	if model == "" {
		t.Skipf("set %s to a model that writes SQL; pull one into the endpoint first", sqlModelEnv)
	}

	writer, err := ollama.NewWriter(base, model, nil)
	if err != nil {
		t.Fatalf("NewWriter(%q, %q) = %v", base, model, err)
	}
	defer writer.CloseIdleConnections()

	table := llm.Table{Name: "applications", Columns: []llm.Column{
		{Name: "stage", Type: "string"},
		{Name: "total", Type: "int"},
	}}

	statement, err := writer.WriteSQL(context.Background(), "how much is on the table at each stage", table)
	if err != nil {
		t.Fatalf("WriteSQL = %v", err)
	}
	if !strings.HasPrefix(strings.ToUpper(statement), "SELECT") {
		t.Errorf("WriteSQL = %q, want a statement beginning with SELECT", statement)
	}
	if !strings.Contains(strings.ToLower(statement), "applications") {
		t.Errorf("WriteSQL = %q, want the table it was shown", statement)
	}
}
