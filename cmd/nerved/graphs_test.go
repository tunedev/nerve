package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The whole path, as a build actually wires it, and with nothing started: a
// text written to a mount is read by the chain the provider config names, and
// an entity read from the same mount comes back with what the text said about
// it. Nothing in the plugin knows there is a nounlink, and nothing in the case
// reaches past the tree.
func TestAGraphMountAnswersThroughTheChainTheConfigNames(t *testing.T) {
	documents := testCorpus(t)
	table := mountGraph(t, documents)

	if err := table.Write(t.Context(), "/graph/docs/stack",
		[]byte("Nerve uses Postgres for storage.")); err != nil {
		t.Fatalf("Write(/graph/docs/stack) = %v", err)
	}

	answered, err := table.Read(t.Context(), "/graph/neighbours/Nerve")
	if err != nil {
		t.Fatalf("Read(/graph/neighbours/Nerve) = %v", err)
	}
	if !strings.Contains(string(answered), "Postgres") {
		t.Errorf("the expansion was %q, want the entity the text related to Nerve", answered)
	}
}

// Blob-persisted, through the wiring the composition root actually uses: a
// second mount table over the same corpus opens the graph the first one left.
func TestAGraphMountedAgainOverTheSameCorpusKeepsWhatItLearnt(t *testing.T) {
	documents := testCorpus(t)

	first := mountGraph(t, documents)
	if err := first.Write(t.Context(), "/graph/docs/stack",
		[]byte("Nerve uses Postgres for storage.")); err != nil {
		t.Fatalf("Write(/graph/docs/stack) = %v", err)
	}

	answered, err := mountGraph(t, documents).Read(t.Context(), "/graph/neighbours/Nerve")
	if err != nil {
		t.Fatalf("Read(/graph/neighbours/Nerve) = %v", err)
	}
	if !strings.Contains(string(answered), "Postgres") {
		t.Errorf("the expansion was %q, want what the first mount was given", answered)
	}
}

// A build whose chain reads no entities has no graph to offer, and the refusal
// lands where an operator is still watching rather than on the first text.
func TestGraphfsMountRefusesAChainThatReadsNoEntities(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, testCorpus(t), nil, testGraphs(t)); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	_, err := registry.Table([]vfs.MountSpec{{
		Prefix: "/graph", Kind: graphfs.Kind, Settings: map[string]string{"name": "notes"},
	}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a graphfs mount with no extracting endpoint = %v, want fs.ErrInvalid", err)
	}
}

// A build that names no corpus has nowhere to keep a graph. It is the same
// refusal a vectorfs mount gets, because the corpus is the same store.
func TestGraphfsMountRefusesABuildWithNoCorpus(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, nil, stubExtractor{}, graphStore(nil)); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	_, err := registry.Table([]vfs.MountSpec{{
		Prefix: "/graph", Kind: graphfs.Kind, Settings: map[string]string{"name": "notes"},
	}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a graphfs mount with no corpus = %v, want fs.ErrInvalid", err)
	}
}

// A graph and a vector index of one name keep their own things, because the
// composition root files a graph's blob under a namespace of its own.
func TestAGraphAndAnIndexOfOneNameDoNotShareAKey(t *testing.T) {
	documents := testCorpus(t)
	table := mountGraph(t, documents)

	if err := table.Write(t.Context(), "/graph/docs/notes", []byte("Nerve uses Postgres.")); err != nil {
		t.Fatalf("Write(/graph/docs/notes) = %v", err)
	}

	if _, err := documents.Head(t.Context(), "notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the graph wrote a key an index named %q would own: %v", "notes", err)
	}
	if _, err := documents.Head(t.Context(), graphPrefix+"notes"); err != nil {
		t.Errorf("Head(%q) = %v, want the graph's blob", graphPrefix+"notes", err)
	}
}

// mountGraph resolves the mount table a config with one graphfs line in it
// produces, through the same registration the composition root does and over
// the endpoint a laptop with no model has.
func mountGraph(t *testing.T, documents vectorfs.DocStore) *vfs.MountTable {
	t.Helper()

	providers := llm.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(providers); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	gateway, err := providers.Gateway([]llm.ProviderSpec{{Name: "entities", Kind: nounlink.Kind}})
	if err != nil {
		t.Fatalf("Gateway() = %v", err)
	}

	registry := vfs.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, documents, gateway.AsEntityExtractor(), graphStore(documents)); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{
		Prefix: "/graph", Kind: graphfs.Kind, Settings: map[string]string{"name": "notes"},
	}})
	if err != nil {
		t.Fatalf("Table(/graph) = %v", err)
	}
	t.Cleanup(func() {
		if err := registry.Release(); err != nil {
			t.Errorf("Release() = %v", err)
		}
	})
	return table
}
