package main

import (
	"context"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/adapters/outbound/termvec"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/recall"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// ingestedName is the corpus this file indexes into, emptied on the way out so a
// run never ranks what the last one left.
const ingestedName = "case_composition_ingested"

// The key contract, end to end: a document finalised through the resource store
// and ingested by the pipeline this build composes is what the context route
// reads back for a turn in the same workspace. The key the index stage writes
// and the key recall reads are only compared here, by the two of them meeting.
func TestAnIngestedDocumentIsInTheContextOfATurnInItsWorkspace(t *testing.T) {
	table, mounts := composedIndex(t)
	ctx := t.Context()

	resources, err := resourceStore(table, resourcesRoot, keyFile(t, "a key this build signs downloads with"))
	if err != nil {
		t.Fatalf("resourceStore() = %v", err)
	}
	ledger, err := indexingLedger(table, ingestRoot)
	if err != nil {
		t.Fatalf("indexingLedger() = %v", err)
	}
	processors := ingest.NewRegistry()
	if err := registerProcessors(processors); err != nil {
		t.Fatalf("registerProcessors() = %v", err)
	}
	indexing, err := indexers(table, mounts, "/vectors", resources)
	if err != nil {
		t.Fatalf("indexers() = %v", err)
	}
	ingestion := pipeline(resources, ledger, processors, indexing...)

	ada := workspace.Scope{User: "ada", Workspace: "hunt"}
	const name = "transcript.txt"
	const words = "she is looking for work in London and will not relocate to Berlin"

	if _, err := resources.Create(ctx, ada, name, "text/plain"); err != nil {
		t.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := resources.Upload(ctx, ada, name, strings.NewReader(words)); err != nil {
		t.Fatalf("Upload(%q) = %v", name, err)
	}
	if _, err := resources.Complete(ctx, ada, name); err != nil {
		t.Fatalf("Complete(%q) = %v", name, err)
	}
	// The job a worker is handed is the path of the meta file Complete wrote.
	declared, err := ada.Path(resourcesRoot, path.Join(name, "meta.json"))
	if err != nil {
		t.Fatalf("Path() = %v", err)
	}
	if err := ingestion.Ingest(ctx, declared); err != nil {
		t.Fatalf("Ingest(%q) = %v", declared, err)
	}

	memories, err := memory.NewStore(table, "/memories", time.Now)
	if err != nil {
		t.Fatalf("NewStore() = %v", err)
	}
	routes, err := recall.NewRoutes(memories, table, "/vectors", "/vectors/docs")
	if err != nil {
		t.Fatalf("NewRoutes() = %v", err)
	}
	got, err := routes.Context(ctx, ada, "will she relocate to Berlin", 0)
	if err != nil {
		t.Fatalf("Context() = %v", err)
	}

	want := recall.Document{Key: name, Body: words}
	if len(got.Documents) != 1 || got.Documents[0] != want {
		ranked, _ := table.Read(ctx, "/vectors/query/will she relocate to Berlin")
		t.Errorf("Documents = %+v, want only %+v; the index ranked %q", got.Documents, want, ranked)
	}
}

// composedIndex is the table a build with resources, a ledger, a memory log and
// a vectorfs index over the offline embedder has, and the mount config it was
// resolved from.
func composedIndex(t *testing.T) (*vfs.MountTable, []vfs.MountSpec) {
	t.Helper()

	index := database(t)
	providers := llm.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
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
	documents, err := corpus("file://" + t.TempDir())
	if err != nil {
		t.Fatalf("corpus() = %v", err)
	}

	mounts := []vfs.MountSpec{
		{Prefix: resourcesRoot, Kind: localfs.Kind, Settings: map[string]string{localfs.RootSetting: t.TempDir()}},
		{Prefix: ingestRoot, Kind: localfs.Kind, Settings: map[string]string{localfs.RootSetting: t.TempDir()}},
		{Prefix: "/memories", Kind: memfs.Kind},
		{Prefix: "/vectors", Kind: vectorfs.Kind, Settings: map[string]string{
			"name": ingestedName, "index_url": index,
			"dimension": strconv.Itoa(termvec.DefaultDimension), "neighbours": "3",
		}},
	}
	registry := vfs.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", nil, vfs.NewTraffic(time.Now), gateway.AsEmbedder(), documents,
		stubExtractor{}, graphStore(documents), nil); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}
	table, err := registry.Table(mounts)
	if err != nil {
		t.Fatalf("Table() = %v", err)
	}
	t.Cleanup(func() {
		if err := table.RemoveAll(context.Background(), "/vectors/docs"); err != nil {
			t.Errorf("RemoveAll(/vectors/docs) = %v", err)
		}
		if err := registry.Release(); err != nil {
			t.Errorf("Release() = %v", err)
		}
	})
	return table, mounts
}
