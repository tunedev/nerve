package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/cuefact"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/adapters/outbound/termvec"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// providerRegistry is one holding every provider kind this build has, under the
// policies a test does not care about.
func providerRegistry(t *testing.T) *llm.Registry {
	t.Helper()

	registry := llm.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(registry); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	return registry
}

// The composition root is the only place that knows a concrete provider, so it
// is the only place a provider kind can be registered — and a kind the config
// can name is the whole point of the registry.
func TestRegisterProvidersMakesTermsumRoutable(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway([]llm.ProviderSpec{{Name: "laptop", Kind: termsum.Kind}})
	if err != nil {
		t.Fatalf("Gateway(laptop) = %v", err)
	}

	if served := gateway.Capabilities(); !slices.Contains(served, "summarise") {
		t.Fatalf("the gateway serves %v, want summarisation", served)
	}

	answer, err := gateway.AsSummariser().Summarise(context.Background(), "The pumps had not run since March. Nobody funded the maintenance.", 1)
	if err != nil {
		t.Fatalf("Summarise = %v", err)
	}
	if answer == "" {
		t.Error("Summarise returned nothing, want a sentence of the document")
	}
}

// termsum reads no settings, so it declares none safe to read back and the
// effective-config surface has nothing of its to show. cuefact has one and it
// is not a credential, so an operator can read back whose turns are extracted,
// and termvec has the width its vectors are, which is the number a mount
// holding them was sized by.
func TestAKindDeclaresWhatIsSafeToReadBack(t *testing.T) {
	if public := providerRegistry(t).Public(termsum.Kind); len(public) != 0 {
		t.Errorf("Public(%q) = %v, want none", termsum.Kind, public)
	}
	if public := providerRegistry(t).Public(cuefact.Kind); !slices.Contains(public, cuefact.RoleSetting) {
		t.Errorf("Public(%q) = %v, want the role", cuefact.Kind, public)
	}
	if public := providerRegistry(t).Public(termvec.Kind); !slices.Contains(public, termvec.DimensionSetting) {
		t.Errorf("Public(%q) = %v, want the dimension", termvec.Kind, public)
	}
}

// The laptop chain embeds. Without an endpoint that does, gateway.AsEmbedder()
// is nil and the vectorfs kind refuses every mount, so a build with no Ollama
// running could not hold an index at all — which is what this endpoint is for.
func TestTheLaptopChainEmbedsWithNothingStarted(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway([]llm.ProviderSpec{
		{Name: "summaries", Kind: termsum.Kind},
		{Name: "vectors", Kind: termvec.Kind},
		{Name: "facts", Kind: cuefact.Kind},
	})
	if err != nil {
		t.Fatalf("Gateway(laptop) = %v", err)
	}

	served := gateway.Capabilities()
	for _, capability := range []string{"summarise", "embed", "extract"} {
		if !slices.Contains(served, capability) {
			t.Fatalf("the gateway serves %v, want %s among them", served, capability)
		}
	}

	embedder := gateway.AsEmbedder()
	if embedder == nil {
		t.Fatal("AsEmbedder = nil; the vectorfs kind refuses a mount on this")
	}

	vectors, err := embedder.Embed(context.Background(), []string{"the pumps had not run", "nobody funded the maintenance"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(vectors) != 2 || len(vectors[0]) != termvec.DefaultDimension {
		t.Errorf("Embed returned %d vectors of %d, want two of %d", len(vectors), len(vectors[0]), termvec.DefaultDimension)
	}
}

// The laptop can commit a conversation with no model configured. One chain
// holds both local endpoints and each capability routes to the one that serves
// it, which is what makes the memory pipeline runnable on a plane.
func TestTheLaptopChainSummarisesAndExtracts(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway([]llm.ProviderSpec{
		{Name: "summaries", Kind: termsum.Kind},
		{Name: "facts", Kind: cuefact.Kind},
	})
	if err != nil {
		t.Fatalf("Gateway(laptop) = %v", err)
	}

	served := gateway.Capabilities()
	if !slices.Contains(served, "summarise") || !slices.Contains(served, "extract") {
		t.Fatalf("the gateway serves %v, want summarisation and extraction", served)
	}

	facts, err := gateway.AsFactExtractor().Extract(context.Background(), []llm.Turn{{Role: "user", Body: "Never force push."}})
	if err != nil {
		t.Fatalf("Extract = %v", err)
	}
	if len(facts) != 1 || facts[0].Kind != "instruction" || facts[0].Priority != llm.High {
		t.Errorf("Extract = %+v, want one high-priority instruction", facts)
	}
}

// Entity extraction runs through the gateway and nowhere else. The chain a
// config document names is what a graph holds, so putting a model in front of
// the laptop's reader is a line of config and nothing that reads entities
// learns of it.
func TestTheLaptopChainReadsEntities(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway([]llm.ProviderSpec{
		{Name: "summaries", Kind: termsum.Kind},
		{Name: "entities", Kind: nounlink.Kind},
	})
	if err != nil {
		t.Fatalf("Gateway(laptop) = %v", err)
	}

	if served := gateway.Capabilities(); !slices.Contains(served, "entities") {
		t.Fatalf("the gateway serves %v, want entity extraction", served)
	}

	found, err := gateway.AsEntityExtractor().ExtractEntities(context.Background(), "Nerve uses Postgres.")
	if err != nil {
		t.Fatalf("ExtractEntities = %v", err)
	}
	if len(found.Entities) != 2 {
		t.Fatalf("ExtractEntities = %+v, want both names", found)
	}
	want := llm.Relation{
		From:      llm.Entity{Kind: nounlink.Mention, Name: "Nerve"},
		Predicate: "uses",
		To:        llm.Entity{Kind: nounlink.Mention, Name: "Postgres"},
	}
	if len(found.Relations) != 1 || found.Relations[0] != want {
		t.Errorf("relations = %+v, want %+v", found.Relations, want)
	}
}

// A build with no providers configured runs and says it serves nothing, which
// is the laptop before anyone has named an endpoint.
func TestABuildWithNoProvidersServesNothing(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway(nil)
	if err != nil {
		t.Fatalf("Gateway(nil) = %v", err)
	}

	if served := gateway.Capabilities(); len(served) != 0 {
		t.Errorf("the gateway serves %v, want nothing", served)
	}
}

// stubOllama stands an embedding endpoint up and returns its address. Every
// vector it answers is the mark it was given, so a test can tell which endpoint
// a routed call reached.
func stubOllama(t *testing.T, mark float32) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var asked struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&asked); err != nil {
			t.Errorf("decoding the request = %v", err)
		}

		vectors := make([][]float32, len(asked.Input))
		for i := range vectors {
			vectors[i] = []float32{mark}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors}); err != nil {
			t.Errorf("encoding the answer = %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// embedded is what one routed call to the chain a config document names comes
// back with, which is the whole of what "swapping a provider is config" has to
// mean: nothing but the settings changes between two runs of this.
func embedded(t *testing.T, base string) float32 {
	t.Helper()

	specs := []llm.ProviderSpec{{
		Name: "embeddings",
		Kind: ollama.Kind,
		Settings: map[string]string{
			ollama.BaseSetting:  base,
			ollama.ModelSetting: "nomic-embed-text",
		},
	}}

	gateway, err := providerRegistry(t).Gateway(specs)
	if err != nil {
		t.Fatalf("Gateway(embeddings) = %v", err)
	}
	if served := gateway.Capabilities(); !slices.Contains(served, "embed") {
		t.Fatalf("the gateway serves %v, want embedding", served)
	}

	vectors, err := gateway.AsEmbedder().Embed(context.Background(), []string{"the pumps had not run"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 1 {
		t.Fatalf("Embed returned %v, want one vector", vectors)
	}
	return vectors[0][0]
}

// Which endpoint an embedding call reaches is a setting and nothing else. The
// two runs here differ by one string; the caller holds the same routed verb and
// cannot tell there was a swap.
func TestSwappingTheEmbeddingEndpointIsConfig(t *testing.T) {
	laptop, hosted := stubOllama(t, 1), stubOllama(t, 2)

	if mark := embedded(t, laptop); mark != 1 {
		t.Errorf("the chain reached the endpoint marking %v, want the laptop's", mark)
	}
	if mark := embedded(t, hosted); mark != 2 {
		t.Errorf("the chain reached the endpoint marking %v, want the hosted one's", mark)
	}
}

// ollama names its endpoint and its model, so an operator can read back which
// model an index was built with. A kind that declared them secret would hide
// the one fact that makes a vector comparable with another.
func TestOllamaReadsItsEndpointAndModelBack(t *testing.T) {
	public := providerRegistry(t).Public(ollama.Kind)
	if !slices.Contains(public, ollama.BaseSetting) || !slices.Contains(public, ollama.ModelSetting) {
		t.Errorf("Public(%q) = %v, want both settings", ollama.Kind, public)
	}
}
