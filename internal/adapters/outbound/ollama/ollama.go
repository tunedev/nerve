// Package ollama reaches an Ollama endpoint for the vectors a similarity search
// compares.
//
// It is one endpoint and one model. Which endpoint answers, and whether it is
// the laptop's or a hosted one, is the gateway's chain and therefore a line of
// config; nothing here knows there is a chain, and nothing above here knows
// there is an Ollama.
//
// It serves embeddings and nothing else. Ollama generates text too, but a
// stage that wants a summary already has one that needs no model, and a verb
// with no caller is a layer that cannot be run.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// embedPath is the endpoint that turns text into vectors. It takes a batch and
// answers one vector per input, which is why the port's verb is a batch.
const embedPath = "/api/embed"

// maxResponseBytes bounds what one answer may be. A batch of vectors has a size
// the caller can predict — the batch times the model's dimension — and anything
// past this is an endpoint that is not the one config named.
const maxResponseBytes = 32 << 20

// Embedder is one Ollama endpoint asked for one model's vectors.
//
// No timeout is set on the client. Every call is made on the caller's context,
// and the gateway has already bounded that: a second timeout here would be a
// second layer deciding how long a model may take.
type Embedder struct {
	endpoint string
	model    string
	client   *http.Client
}

// New returns an embedder over base for model. It resolves the URL here so a
// misspelled endpoint is a boot failure rather than the first document's.
func New(base, model string, client *http.Client) (*Embedder, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("endpoint %q: %w", base, fs.ErrInvalid)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("endpoint %q: want an http or https URL: %w", base, fs.ErrInvalid)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("endpoint %q: want a host: %w", base, fs.ErrInvalid)
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("endpoint %q: want a model to embed with: %w", base, fs.ErrInvalid)
	}
	if client == nil {
		client = &http.Client{}
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + embedPath
	return &Embedder{endpoint: parsed.String(), model: model, client: client}, nil
}

// CloseIdleConnections drops the connections the client is holding open. A
// provider is built once and lives as long as the process, so this is a test's
// business rather than an operator's.
func (e *Embedder) CloseIdleConnections() { e.client.CloseIdleConnections() }

// Embed returns one vector per text, in the order the texts were given.
//
// No texts is no call. A caller batching a corpus reaches the end of it, and
// asking a model to embed nothing is a round trip that can only fail.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(request{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embed with %q: %w", e.model, err)
	}

	answered, err := e.post(ctx, body)
	if err != nil {
		return nil, err
	}

	// A vector is paired with its text by position and by nothing else, so an
	// endpoint that answered a different number of them has already lost the
	// pairing. Handing back what it said would index each document under
	// another document's meaning.
	if len(answered.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed %d texts with %q: the endpoint answered %d vectors: %w", len(texts), e.model, len(answered.Embeddings), fs.ErrInvalid)
	}
	return answered.Embeddings, nil
}

// request is what /api/embed is asked. Input is a batch, which is the shape
// that makes one round trip serve a corpus.
type request struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// response is what it answers. Everything else it reports is timing.
type response struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// post makes one call and reads the vectors out of it.
func (e *Embedder) post(ctx context.Context, body []byte) (response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return response{}, fmt.Errorf("embed with %q: %w", e.model, err)
	}
	request.Header.Set("Content-Type", "application/json")

	answer, err := e.client.Do(request)
	if err != nil {
		return response{}, fmt.Errorf("embed with %q: %w: %w", e.model, err, vfs.ErrTransient)
	}
	defer drain(answer)

	if answer.StatusCode >= http.StatusBadRequest {
		return response{}, statusError(e.model, answer)
	}

	var answered response
	if err := json.NewDecoder(io.LimitReader(answer.Body, maxResponseBytes)).Decode(&answered); err != nil {
		return response{}, fmt.Errorf("embed with %q: %w: %w", e.model, err, fs.ErrInvalid)
	}
	return answered, nil
}

// statusError translates one refusal into the vocabulary the gateway routes on.
// The transient mark goes on here, at the adapter's edge, rather than leaving
// the classifier to learn Ollama's status codes.
//
// A model the endpoint has not pulled is fs.ErrNotExist and stops the chain,
// which is deliberate. It is a config answer rather than a wobble, and passing
// over it would fill an index with vectors from whichever model the next
// endpoint happened to run — comparable with nothing already in it.
func statusError(model string, answer *http.Response) error {
	where := fmt.Sprintf("embed with %q: %s", model, answer.Status)

	switch {
	case answer.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: the endpoint has no such model, try pulling it: %w", where, fs.ErrNotExist)
	case answer.StatusCode == http.StatusForbidden, answer.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", where, fs.ErrPermission)
	case answer.StatusCode == http.StatusTooManyRequests, answer.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%s: %w", where, vfs.ErrTransient)
	default:
		return fmt.Errorf("%s: %w", where, fs.ErrInvalid)
	}
}

// drain reads what is left of an answer and closes it, so the connection goes
// back to the pool instead of being dropped.
func drain(answer *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(answer.Body, 1<<16))
	_ = answer.Body.Close()
}
