package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/hybridfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The wiring, with nothing started: a fusion mounted beside two rankers reads
// their answers through the table it is itself in, and the key both of them
// named comes back first.
//
// The rankers here are two graphs rather than a graph and an index, because a
// vector index needs a database and this asserts the composition rather than
// the ranking. What it holds is the part no plugin's own cases can: that the
// tree a fusion reads through is the one every other caller writes to, and that
// a mount built before the table can still read it afterwards.
func TestAFusionAnswersFromTheMountsItIsMountedBeside(t *testing.T) {
	table := mountFusion(t)
	ctx := t.Context()

	// Each graph puts a text of its own first and the shared text second, so
	// the fused answer can only lead with the shared one by having read both
	// rankings.
	for path, text := range map[string]string{
		"/left/docs/alpha":   "Nerve uses Postgres for storage.",
		"/left/docs/shared":  "Postgres was mentioned.",
		"/right/docs/beta":   "Rig uses Postgres daily.",
		"/right/docs/shared": "Postgres was mentioned.",
	} {
		if err := table.Write(ctx, path, []byte(text)); err != nil {
			t.Fatalf("Write(%q) = %v", path, err)
		}
	}

	answered, err := table.Read(ctx, "/retrieval/query/Postgres")
	if err != nil {
		t.Fatalf("Read(/retrieval/query/Postgres) = %v", err)
	}

	rows := strings.Split(strings.TrimSuffix(string(answered), "\n"), "\n")
	if len(rows) < 2 {
		t.Fatalf("the fused answer was %q, want a heading and the texts both graphs named", answered)
	}
	if first, _, _ := strings.Cut(rows[1], "\t"); first != "shared" {
		t.Errorf("the fused answer put %q first, want the key both graphs named", first)
	}
	if !strings.Contains(rows[1], "left=2,right=2") {
		t.Errorf("the first row was %q, want where each graph put the key", rows[1])
	}
}

// A fusion is written through nowhere. Every key it names belongs to a graph,
// and that is where a text is written.
func TestAFusionInTheTableRefusesAWrite(t *testing.T) {
	if err := mountFusion(t).Write(t.Context(), "/retrieval/query/Postgres", []byte("no")); err == nil {
		t.Error("Write(/retrieval/query/Postgres) = nil, want a refusal")
	}
}

// mountFusion resolves the mount table a config with two graphfs lines and one
// hybridfs line in it produces, through the same registration and the same
// indirection the composition root uses.
func mountFusion(t *testing.T) *vfs.MountTable {
	t.Helper()

	providers := llm.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(providers); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	gateway, err := providers.Gateway([]llm.ProviderSpec{{Name: "entities", Kind: nounlink.Kind}})
	if err != nil {
		t.Fatalf("Gateway() = %v", err)
	}

	documents := testCorpus(t)
	fused := &grown{}
	registry := vfs.NewRegistry(time.Minute, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, documents, gateway.AsEntityExtractor(), graphStore(documents), fused.tree); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
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
