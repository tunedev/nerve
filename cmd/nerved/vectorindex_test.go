package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/adapters/outbound/termvec"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// These turn this file on. The whole path needs both ends of it — a model to
// make vectors and a database to compare them — so nothing here runs without
// them, and the semantics stay asserted offline in the plugin's own package.
//
// `make up` starts what these point at. The model has to be pulled into it:
// `docker compose exec ollama ollama pull nomic-embed-text`.
const (
	indexEnv = "NERVE_VECTORFS_INDEX"
	modelEnv = "NERVE_OLLAMA_ENDPOINT"
)

// The corpora this file writes into, one per case so two runs of the file do
// not rank each other's documents. Each is emptied on the way out rather than
// made fresh each run, because a corpus that outlives the process cannot be
// emptied by making a new one.
const (
	indexName  = "case_composition"
	laptopName = "case_composition_laptop"
)

// vectorDimension is how wide nomic-embed-text's vectors are, which is what a
// mount over it declares. Nothing in the process can discover it: Embed reports
// no width, so the column is sized from config and this is that config.
const vectorDimension = "768"

// The whole path, as a build actually wires it: a document written to a mount
// is embedded by the chain the provider config names and stored in the database
// the mount config names, and a question read from the same mount comes back as
// that corpus ranked. Nothing in the plugin knows there is an Ollama, and
// nothing in the test reaches past the tree.
func TestAVectorMountAnswersThroughTheChainTheConfigNames(t *testing.T) {
	index, endpoint := stack(t)

	providers := llm.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(providers); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	gateway, err := providers.Gateway([]llm.ProviderSpec{{
		Name: "embeddings",
		Kind: ollama.Kind,
		Settings: map[string]string{
			ollama.BaseSetting:  endpoint,
			ollama.ModelSetting: "nomic-embed-text",
		},
	}})
	if err != nil {
		t.Fatalf("Gateway() = %v", err)
	}

	table := mountIndex(t, index, indexName, vectorDimension, gateway.AsEmbedder())
	ctx := t.Context()

	corpus := map[string]string{
		"retry":   "a retry repeats a call that failed for a reason that may pass",
		"breaker": "a circuit breaker stops calling a dependency that keeps failing",
		"pasta":   "boil the water before the pasta goes in",
	}
	for key, text := range corpus {
		if err := table.Write(ctx, "/memories/docs/"+key, []byte(text)); err != nil {
			t.Fatalf("Write(/memories/docs/%s) = %v", key, err)
		}
	}

	answered, err := table.Read(ctx, "/memories/query/what happens when a call fails")
	if err != nil {
		t.Fatalf("Read(/memories/query/...) = %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(answered), "\n"), "\n")
	if len(lines) != len(corpus)+1 {
		t.Fatalf("the answer was %q, want a heading and three documents", answered)
	}
	if furthest, _, _ := strings.Cut(lines[len(lines)-1], "\t"); furthest != "pasta" {
		t.Errorf("the furthest document was %q, want %q", furthest, "pasta")
	}
	if closest, _, _ := strings.Cut(lines[1], "\t"); closest == "pasta" {
		t.Errorf("the closest document was %q, want one about failing calls", closest)
	}
}

// mountIndex resolves the mount table a config with one vectorfs line in it
// produces, through the same registration the composition root does.
func mountIndex(t *testing.T, index, name, dimension string, embed llm.Embedder) *vfs.MountTable {
	t.Helper()

	documents, err := corpus("file://" + t.TempDir())
	if err != nil {
		t.Fatalf("corpus() = %v", err)
	}

	registry := vfs.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", nil, vfs.NewTraffic(time.Now), embed, documents, stubExtractor{}, graphStore(documents)); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{
		Prefix: "/memories",
		Kind:   vectorfs.Kind,
		Settings: map[string]string{
			"name": name, "index_url": index, "dimension": dimension, "neighbours": "3",
		},
	}})
	if err != nil {
		t.Fatalf("Table(/memories) = %v", err)
	}
	// The corpus is emptied on a context of its own: the case's is already
	// cancelled by the time cleanup runs, and a corpus left behind would be
	// what the next run ranks.
	t.Cleanup(func() {
		if err := table.RemoveAll(context.Background(), "/memories/docs"); err != nil {
			t.Errorf("RemoveAll(/memories/docs) = %v", err)
		}
		if err := registry.Release(); err != nil {
			t.Errorf("Release() = %v", err)
		}
	})
	return table
}

// database is the index this file keeps vectors in, or the reason there is no
// run. It is the only end the laptop case needs: termvec is in the process.
func database(t *testing.T) string {
	t.Helper()

	index := os.Getenv(indexEnv)
	if index == "" {
		t.Skipf("set %s to run against a database; `make up` starts one", indexEnv)
	}
	return index
}

// stack is the two ends the model case needs, or the reason there is no run.
func stack(t *testing.T) (index, endpoint string) {
	t.Helper()

	index = database(t)
	endpoint = os.Getenv(modelEnv)
	if endpoint == "" {
		t.Skipf("set %s to run against an Ollama; `make up` starts one", modelEnv)
	}
	// The chain hands out a routed Embedder, not the adapter, so the endpoint's
	// keep-alive connections are given back through the transport they were
	// taken from. A pooled connection is a goroutine the leak check finds still
	// running once the tests are over.
	t.Cleanup(http.DefaultTransport.(*http.Transport).CloseIdleConnections)
	return index, endpoint
}

// The same path with nothing started but the database, which is what the
// offline embedder is for: a build that reaches no model still mounts an index
// and still ranks a corpus. Nothing in the config differs from the case above
// but the provider kind and the width it is sized for.
func TestALaptopWithNoModelStillMountsAnIndex(t *testing.T) {
	index := database(t)

	providers := llm.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(providers); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	gateway, err := providers.Gateway([]llm.ProviderSpec{
		{Name: "summaries", Kind: termsum.Kind},
		{Name: "vectors", Kind: termvec.Kind},
	})
	if err != nil {
		t.Fatalf("Gateway() = %v", err)
	}

	table := mountIndex(t, index, laptopName, strconv.Itoa(termvec.DefaultDimension), gateway.AsEmbedder())
	ctx := t.Context()

	corpus := map[string]string{
		"retry":   "a retry repeats a call that failed for a reason that may pass",
		"breaker": "a circuit breaker stops calling a dependency that keeps failing",
		"pasta":   "boil the water before the pasta goes in",
	}
	for key, text := range corpus {
		if err := table.Write(ctx, "/memories/docs/"+key, []byte(text)); err != nil {
			t.Fatalf("Write(/memories/docs/%s) = %v", key, err)
		}
	}

	answered, err := table.Read(ctx, "/memories/query/a call that keeps failing")
	if err != nil {
		t.Fatalf("Read(/memories/query/...) = %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(answered), "\n"), "\n")
	if len(lines) != len(corpus)+1 {
		t.Fatalf("the answer was %q, want a heading and three documents", answered)
	}
	if furthest, _, _ := strings.Cut(lines[len(lines)-1], "\t"); furthest != "pasta" {
		t.Errorf("the furthest document was %q, want %q", furthest, "pasta")
	}
}
