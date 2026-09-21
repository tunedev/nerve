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
	"github.com/tunedev/nerve/internal/core/stability"
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

// The acceptance of E7.6, through the wiring the composition root uses: a mount
// whose config says depth 2 answers for Nerve with Disk, two hops out, and with
// nothing about the Rig, which Furiosa owns and no text joins to Nerve.
func TestAGraphMountOfDepthTwoAnswersTwoHopsOut(t *testing.T) {
	table := mountGraphWith(t, testCorpus(t), map[string]string{"name": "notes", "depth": "2"})
	for key, text := range map[string]string{
		"stack": "Nerve uses Postgres for storage.",
		"disk":  "Postgres runs on Disk.",
		"crew":  "Furiosa owns Rig.",
	} {
		if err := table.Write(t.Context(), "/graph/docs/"+key, []byte(text)); err != nil {
			t.Fatalf("Write(/graph/docs/%s) = %v", key, err)
		}
	}

	answered, err := table.Read(t.Context(), "/graph/neighbours/Nerve")
	if err != nil {
		t.Fatalf("Read(/graph/neighbours/Nerve) = %v", err)
	}
	t.Logf("Read(/graph/neighbours/Nerve) =\n%s", answered)
	if !strings.Contains(string(answered), "Disk") {
		t.Errorf("the expansion was %q, want Disk, two hops out", answered)
	}
	if strings.Contains(string(answered), "Rig") {
		t.Errorf("the expansion was %q, want nothing about the Rig", answered)
	}
	if crew, _ := table.Read(t.Context(), "/graph/neighbours/Furiosa"); !strings.Contains(string(crew), "Rig") {
		t.Errorf("the expansion of Furiosa was %q, want the Rig, or the case above proves nothing", crew)
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

// The acceptance of E7.11, through the wiring the composition root uses and
// over a corpus on the host disk: a snapshot taken by one mount table is what a
// later one is set back to, with everything written in between gone.
func TestASnapshotTakenByOneMountIsRestoredByALaterOne(t *testing.T) {
	documents := testCorpus(t)

	first := mountGraph(t, documents)
	if err := first.Write(t.Context(), "/graph/docs/stack",
		[]byte("Nerve uses Postgres for storage.")); err != nil {
		t.Fatalf("Write(/graph/docs/stack) = %v", err)
	}
	if err := first.Write(t.Context(), "/graph/snapshots/before", []byte("about to reindex")); err != nil {
		t.Fatalf("Write(/graph/snapshots/before) = %v", err)
	}
	if err := first.Write(t.Context(), "/graph/docs/crew", []byte("Furiosa drives the Rig.")); err != nil {
		t.Fatalf("Write(/graph/docs/crew) = %v", err)
	}

	later := mountGraph(t, documents)
	if err := later.Write(t.Context(), "/graph/restore", []byte("before")); err != nil {
		t.Fatalf("Write(/graph/restore) = %v", err)
	}

	if _, err := later.Read(t.Context(), "/graph/docs/crew"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/graph/docs/crew) = %v, want a text the snapshot never had to be gone", err)
	}
	body, err := later.Read(t.Context(), "/graph/docs/stack")
	if err != nil {
		t.Fatalf("Read(/graph/docs/stack) = %v", err)
	}
	if string(body) != "Nerve uses Postgres for storage." {
		t.Errorf("Read(/graph/docs/stack) = %q, want the text as the snapshot held it", body)
	}

	standing, err := mountGraph(t, documents).Read(t.Context(), "/graph/restore")
	if err != nil {
		t.Fatalf("Read(/graph/restore) = %v", err)
	}
	if !strings.Contains(string(standing), "standing before") {
		t.Errorf("Read(/graph/restore) = %q, want the label the graph stands at", standing)
	}
}

// A snapshot is filed apart from the graph, so a graph and a label of one name
// are two keys in the corpus rather than one.
func TestASnapshotIsFiledApartFromTheGraph(t *testing.T) {
	documents := testCorpus(t)
	table := mountGraph(t, documents)

	if err := table.Write(t.Context(), "/graph/snapshots/notes", nil); err != nil {
		t.Fatalf("Write(/graph/snapshots/notes) = %v", err)
	}

	if _, err := documents.Head(t.Context(), graphPrefix+"notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a snapshot wrote the key the graph itself is kept under: %v", err)
	}
	if _, err := documents.Head(t.Context(), snapshotKey("notes", "notes")); err != nil {
		t.Errorf("Head(%q) = %v, want the snapshot", snapshotKey("notes", "notes"), err)
	}
}

// A build whose chain reads no entities has no graph to offer, and the refusal
// lands where an operator is still watching rather than on the first text.
func TestGraphfsMountRefusesAChainThatReadsNoEntities(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, testCorpus(t), nil, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
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
	registry := vfs.NewRegistry(time.Second, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, nil, stubExtractor{}, graphStore(nil), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
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
	return mountGraphWith(t, documents, map[string]string{"name": "notes"})
}

// mountGraphWith is mountGraph with the mount's settings spelled by the case.
func mountGraphWith(t *testing.T, documents vectorfs.DocStore, settings map[string]string) *vfs.MountTable {
	t.Helper()

	providers := llm.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(providers); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	gateway, err := providers.Gateway([]llm.ProviderSpec{{Name: "entities", Kind: nounlink.Kind}})
	if err != nil {
		t.Fatalf("Gateway() = %v", err)
	}

	registry := vfs.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, documents, gateway.AsEntityExtractor(), graphStore(documents), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{
		Prefix: "/graph", Kind: graphfs.Kind, Settings: settings,
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
