package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/hybridfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/recall"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The context route over the mounts the composition root builds: a memory put
// into the log and a document put into the graphs both come back as the context
// of a turn that asks about the document, and a document in another workspace
// that the graphs rank just as high does not.
//
// Both graphs hold the same documents, as two rankers over one corpus do, and
// the corpus the bodies are read from is one of them.
func TestATurnsContextHoldsAMemoryAndADocumentThatWerePutIn(t *testing.T) {
	table := mountRecall(t)
	ctx := t.Context()
	ada := workspace.Scope{User: "ada", Workspace: "hunt"}

	memories, err := memory.NewStore(table, "/memories", time.Now)
	if err != nil {
		t.Fatalf("NewStore = %v", err)
	}
	tabs := memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal}
	if _, err := memories.Add(ctx, ada, "chat", []memory.Fact{tabs}); err != nil {
		t.Fatalf("Add = %v", err)
	}
	for key, text := range map[string]string{
		"ada/hunt/cv":     "Nerve uses Postgres for storage.",
		"bob/hunt/salary": "Rig uses Postgres daily.",
	} {
		for _, graph := range []string{"/left", "/right"} {
			if err := table.Write(ctx, graph+"/docs/"+key, []byte(text)); err != nil {
				t.Fatalf("Write(%s %q) = %v", graph, key, err)
			}
		}
	}

	ranked, err := table.Read(ctx, "/retrieval/query/Postgres")
	if err != nil {
		t.Fatalf("Read(/retrieval/query/Postgres) = %v", err)
	}
	if !strings.Contains(string(ranked), "bob/hunt/salary") {
		t.Fatalf("the fusion answered %q, want it to rank the other workspace's document too", ranked)
	}

	routes, err := recall.NewRoutes(memories, table, "/retrieval", "/left/docs")
	if err != nil {
		t.Fatalf("NewRoutes = %v", err)
	}
	got, err := routes.Context(ctx, ada, "Postgres", 0)
	if err != nil {
		t.Fatalf("Context = %v", err)
	}
	t.Logf("Context(ada, Postgres) = %+v", got)

	if len(got.Memories) != 1 || got.Memories[0].Fact != tabs {
		t.Errorf("Memories = %+v, want the one fact put in", got.Memories)
	}
	want := recall.Document{Key: "cv", Body: "Nerve uses Postgres for storage."}
	if len(got.Documents) != 1 || got.Documents[0] != want {
		t.Errorf("Documents = %+v, want only %+v", got.Documents, want)
	}
}

// mountRecall resolves the table a config with a memory mount, two graphs, and
// a fusion over their mentions produces, through the registration and the
// indirection the composition root uses.
func mountRecall(t *testing.T) *vfs.MountTable {
	t.Helper()

	providers := llm.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(providers); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	gateway, err := providers.Gateway([]llm.ProviderSpec{{Name: "entities", Kind: nounlink.Kind}})
	if err != nil {
		t.Fatalf("Gateway() = %v", err)
	}

	documents := testCorpus(t)
	fused := &grown{}
	registry := vfs.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, documents, gateway.AsEntityExtractor(), graphStore(documents), fused.tree); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/memories", Kind: memfs.Kind},
		{Prefix: "/left", Kind: graphfs.Kind, Settings: map[string]string{"name": "left"}},
		{Prefix: "/right", Kind: graphfs.Kind, Settings: map[string]string{"name": "right"}},
		{Prefix: "/retrieval", Kind: hybridfs.Kind, Settings: map[string]string{
			"name":    "retrieval",
			"sources": "left=/left/mentions;right=/right/mentions",
		}},
	})
	if err != nil {
		t.Fatalf("Table() = %v", err)
	}
	fused.table = table
	return table
}
