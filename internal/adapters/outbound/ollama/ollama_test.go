package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// asked is what the endpoint was sent, recorded so a test can check the wire
// rather than a mock's call count.
type asked struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// endpoint stands an Ollama in front of the adapter, so every test here is a
// real request over a real connection and the wire format is what is under
// test. handle answers one call.
func endpoint(t *testing.T, handle http.HandlerFunc) *ollama.Embedder {
	t.Helper()

	server := httptest.NewServer(handle)
	t.Cleanup(server.Close)

	embedder, err := ollama.New(server.URL, "nomic-embed-text", server.Client())
	if err != nil {
		t.Fatalf("New(%q) = %v", server.URL, err)
	}
	t.Cleanup(embedder.CloseIdleConnections)
	return embedder
}

// answering replies with one vector per input, and records what it was asked.
func answering(t *testing.T, seen *asked) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("the adapter used %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(seen); err != nil {
			t.Errorf("decoding the request = %v", err)
		}

		vectors := make([][]float32, len(seen.Input))
		for i := range seen.Input {
			vectors[i] = []float32{float32(i), 0.5}
		}
		writeJSON(t, w, map[string]any{"embeddings": vectors})
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encoding the answer = %v", err)
	}
}

// One call carries the whole batch and answers one vector per text, in the
// order the texts were given. That pairing is positional and is the whole
// contract: an index that gets it wrong files each document under another
// document's meaning.
func TestEmbedSendsOneBatchAndPairsVectorsByPosition(t *testing.T) {
	var seen asked
	var path string
	embedder := endpoint(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		answering(t, &seen)(w, r)
	})

	vectors, err := embedder.Embed(context.Background(), []string{"the pumps", "the grid"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}

	if path != "/api/embed" {
		t.Errorf("the adapter posted to %q, want /api/embed", path)
	}
	if seen.Model != "nomic-embed-text" {
		t.Errorf("the endpoint was asked for model %q, want nomic-embed-text", seen.Model)
	}
	if len(seen.Input) != 2 || seen.Input[0] != "the pumps" || seen.Input[1] != "the grid" {
		t.Errorf("the endpoint was sent %v, want both texts in one call", seen.Input)
	}
	if len(vectors) != 2 {
		t.Fatalf("Embed returned %d vectors, want 2", len(vectors))
	}
	if vectors[0][0] != 0 || vectors[1][0] != 1 {
		t.Errorf("Embed returned %v, want the endpoint's vectors in order", vectors)
	}
}

// No texts is no call. A caller batching a corpus reaches the end of it, and a
// round trip that can only fail is not worth making.
func TestEmbedNothingMakesNoCall(t *testing.T) {
	called := false
	embedder := endpoint(t, func(http.ResponseWriter, *http.Request) { called = true })

	vectors, err := embedder.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed(nil) = %v", err)
	}
	if len(vectors) != 0 {
		t.Errorf("Embed(nil) returned %d vectors, want none", len(vectors))
	}
	if called {
		t.Error("the endpoint was called, want no round trip for no texts")
	}
}

// An endpoint that answers a different number of vectors has lost the pairing,
// so the answer is refused rather than handed on misaligned.
func TestAVectorCountThatDoesNotMatchIsRefused(t *testing.T) {
	embedder := endpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"embeddings": [][]float32{{0.5}}})
	})

	_, err := embedder.Embed(context.Background(), []string{"the pumps", "the grid"})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Embed = %v, want %v", err, fs.ErrInvalid)
	}
}

// A model the endpoint has not pulled stops the chain rather than passing to
// the next endpoint. It is a config answer, and falling through would fill an
// index with vectors from whichever model answered next.
func TestAMissingModelIsAnAnswerAndNotAWobble(t *testing.T) {
	embedder := endpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	})

	_, err := embedder.Embed(context.Background(), []string{"the pumps"})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Embed = %v, want %v", err, fs.ErrNotExist)
	}
	if got := vfs.Classify(err); got != vfs.FailFast {
		t.Errorf("Classify = %s, want %s", got, vfs.FailFast)
	}
}

// An endpoint that is busy or broken is the one failure worth another attempt
// and another endpoint, so it carries the mark the classifier sorts on.
func TestABusyEndpointIsTransient(t *testing.T) {
	for name, status := range map[string]int{
		"overloaded":  http.StatusTooManyRequests,
		"broken":      http.StatusInternalServerError,
		"unreachable": http.StatusServiceUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			embedder := endpoint(t, func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "no", status)
			})

			_, err := embedder.Embed(context.Background(), []string{"the pumps"})
			if got := vfs.Classify(err); got != vfs.Retryable {
				t.Errorf("Embed = %v, classified %s, want %s", err, got, vfs.Retryable)
			}
		})
	}
}

// An endpoint that cannot be reached at all is transient too, which is Ollama
// not running on the laptop it was configured for.
func TestAnUnreachableEndpointIsTransient(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	base := server.URL
	server.Close()

	embedder, err := ollama.New(base, "nomic-embed-text", nil)
	if err != nil {
		t.Fatalf("New(%q) = %v", base, err)
	}
	defer embedder.CloseIdleConnections()

	_, err = embedder.Embed(context.Background(), []string{"the pumps"})
	if got := vfs.Classify(err); got != vfs.Retryable {
		t.Errorf("Embed = %v, classified %s, want %s", err, got, vfs.Retryable)
	}
}

// A misspelled endpoint fails where it is built, not on the first document.
func TestAMisspelledEndpointFailsAtBuild(t *testing.T) {
	for name, base := range map[string]string{
		"no scheme": "localhost:11434",
		"no host":   "http://",
		"not a URL": "http://%zz",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ollama.New(base, "nomic-embed-text", nil); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("New(%q) = %v, want %v", base, err, fs.ErrInvalid)
			}
		})
	}
}

// An endpoint with no model named embeds nothing, so it is refused where it is
// built rather than asked for a nameless model.
func TestAnEndpointWithNoModelFailsAtBuild(t *testing.T) {
	if _, err := ollama.New("http://localhost:11434", "  ", nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no model = %v, want %v", err, fs.ErrInvalid)
	}
}

// A base URL with a path of its own keeps it, which is Ollama behind a reverse
// proxy that mounts it under a prefix.
func TestABaseWithAPathKeepsIt(t *testing.T) {
	var seen asked
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/api/embed" {
			t.Errorf("the adapter posted to %q, want /models/api/embed", r.URL.Path)
		}
		answering(t, &seen)(w, r)
	}))
	defer server.Close()

	embedder, err := ollama.New(server.URL+"/models", "nomic-embed-text", server.Client())
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	defer embedder.CloseIdleConnections()

	if _, err := embedder.Embed(context.Background(), []string{"the pumps"}); err != nil {
		t.Fatalf("Embed = %v", err)
	}
}
