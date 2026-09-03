package ollama_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
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

// A real model answers one vector per text, every vector the same width, and
// answers the same text the same way twice. That last one is what makes an
// index worth keeping: a vector written on Monday is compared against one read
// on Friday.
func TestLiveEmbedAnswersOneVectorPerText(t *testing.T) {
	embedder := live(t)

	texts := []string{"the pumps had not run since March", "nobody funded the maintenance"}
	vectors, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(vectors) != len(texts) {
		t.Fatalf("Embed returned %d vectors for %d texts", len(vectors), len(texts))
	}
	if len(vectors[0]) == 0 {
		t.Fatal("Embed returned an empty vector")
	}
	if len(vectors[0]) != len(vectors[1]) {
		t.Fatalf("Embed returned vectors of %d and %d dimensions, want one width", len(vectors[0]), len(vectors[1]))
	}

	again, err := embedder.Embed(context.Background(), texts[:1])
	if err != nil {
		t.Fatalf("Embed again = %v", err)
	}
	if again[0][0] != vectors[0][0] {
		t.Errorf("the same text embedded to %v and then %v, want one answer", vectors[0][0], again[0][0])
	}
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
