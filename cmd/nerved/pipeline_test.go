package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/search"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// These turn this file on. The whole path needs both ends of a queue — a
// journal that records a job and a broker that says one is there — so nothing
// here runs without them, and the pipeline's semantics stay asserted offline in
// its own package.
//
// `make up` starts what these point at; `make test-queue` sets them.
const (
	journalEnv = "NERVE_QUEUEFS_JOURNAL"
	brokerEnv  = "NERVE_QUEUEFS_BROKER"
)

// The queue this file publishes through. It is one name rather than one per run
// so that runs do not leave durable queues behind on the broker; what a run
// leaves in the journal it takes with it.
const (
	queueName     = "case_composition_ingest"
	queueExchange = "nerve.test.composition.ingest"
	queuePrefix   = "/queue"
)

// The roots the composed build is given.
const (
	resourcesRoot = "/resources"
	ingestRoot    = "/ingest"
)

// ingested is how long a case waits for a job to go round — observer, journal,
// broker, worker, pipeline — and asked is how often it looks. A stack on the
// same machine does it in milliseconds; the wait is long enough that a loaded
// one still does, and short enough that an ingestion that will never happen is
// a failure rather than a hung run.
const (
	ingested = 20 * time.Second
	asked    = 50 * time.Millisecond
)

// This case is the one place the defect in nrv-93kh is visible: a job the broker
// acknowledged is dropped when the relay's carried_at update still holds the row
// the worker is claiming. The failure below names it, because a red run here is
// otherwise a wiring hunt for something that is wired.
//
// The acceptance for this wiring, as a running build actually composes it: a
// document finalised through the resource store is ingested by a worker on the
// far end of a real queue, and the words it left are what a question in the
// caller's own words finds. Nothing in the uploading half asks for any of it.
func TestAFinalisedDocumentIsIngestedAndFoundByMeaning(t *testing.T) {
	table, mounts := composed(t)

	resources, err := resourceStore(uploads(table, queuePrefix), resourcesRoot, keyFile(t, "a key this build signs downloads with"))
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

	consuming, err := consumer(table, mounts, queuePrefix, resourcesRoot, resources, ledger, processors)
	if err != nil {
		t.Fatalf("consumer() = %v", err)
	}
	running, stop := context.WithCancel(t.Context())
	consuming.start(running)
	t.Cleanup(func() {
		stop()
		if err := consuming.stop(); err != nil {
			t.Errorf("stop() = %v", err)
		}
	})

	ctx := t.Context()
	scope := workspace.Scope{User: "ada", Workspace: "hunt"}
	const name = "transcript.txt"
	const words = "she is looking for work in London and will not relocate to Berlin"

	if _, err := resources.Create(ctx, scope, name, "text/plain"); err != nil {
		t.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := resources.Upload(ctx, scope, name, strings.NewReader(words)); err != nil {
		t.Fatalf("Upload(%q) = %v", name, err)
	}
	if _, err := resources.Complete(ctx, scope, name); err != nil {
		t.Fatalf("Complete(%q) = %v", name, err)
	}

	// Nothing above asked for ingestion. What the caller waits on here is only
	// what a caller would ask afterwards: whether its document is searchable.
	indexing := waitForIndexing(t, ledger, scope, name)
	if indexing.Status != ingest.Indexed {
		t.Fatalf("the ledger says %q (stage %q: %s), want %q",
			indexing.Status, indexing.Stage, indexing.Failure, ingest.Indexed)
	}

	left, err := resources.Derived(ctx, scope, name, ingest.TextSidecar)
	if err != nil {
		t.Fatalf("Derived(%q) = %v", ingest.TextSidecar, err)
	}
	if string(left) != words {
		t.Errorf("the ingestion left %q, want the document's words", left)
	}

	routes, err := search.NewRoutes(table, termmatch.NewRanker())
	if err != nil {
		t.Fatalf("NewRoutes() = %v", err)
	}
	hits, err := routes.Find(ctx, scope, resourcesRoot, "will she relocate to Berlin", 0)
	if err != nil {
		t.Fatalf("Find() = %v", err)
	}
	if !found(hits, name+"/"+ingest.TextSidecar) {
		t.Errorf("Find returned %v, want the words the ingestion left", paths(hits))
	}
}

// waitForIndexing asks the ledger where the document stands until the run it
// records has ended, which is the only thing a caller can do: the call that
// finalised the upload returned before any of this started.
//
// Working is not an end. It is written before the first stage runs, so a case
// that stopped there would be reading the pipeline starting rather than the
// pipeline finishing.
func waitForIndexing(t *testing.T, ledger *ingest.Ledger, s workspace.Scope, name string) ingest.Indexing {
	t.Helper()

	deadline := time.Now().Add(ingested)
	for {
		indexing, err := ledger.Get(t.Context(), s, name)
		if err != nil {
			t.Fatalf("Get(%q) = %v", name, err)
		}
		if indexing.Status == ingest.Indexed || indexing.Status == ingest.Failed {
			return indexing
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing ingested %q in %s: either the observer, the queue and the worker are not wired, "+
				"or the job was lost to nrv-93kh - the carried_at update racing the claim", name, ingested)
		}
		time.Sleep(asked)
	}
}

// composed is the mount table this file's build has, and the mount config it
// was resolved from — which is what the listener reaches the broker with.
func composed(t *testing.T) (*vfs.MountTable, []vfs.MountSpec) {
	t.Helper()

	mounts := []vfs.MountSpec{
		{Prefix: resourcesRoot, Kind: localfs.Kind, Settings: map[string]string{localfs.RootSetting: t.TempDir()}},
		{Prefix: ingestRoot, Kind: localfs.Kind, Settings: map[string]string{localfs.RootSetting: t.TempDir()}},
		{Prefix: queuePrefix, Kind: queuefs.Kind, Settings: map[string]string{
			"name": queueName, "exchange": queueExchange,
			"journal_url": stackSetting(t, journalEnv), "broker_url": stackSetting(t, brokerEnv),
		}},
	}

	registry := vfs.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", nil, vfs.NewTraffic(time.Now), nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}
	table, err := registry.Table(mounts)
	if err != nil {
		t.Fatalf("Table() = %v", err)
	}
	// The queue outlives the process, so what a run left in it is what the next
	// run's worker would claim. It is emptied on a context of its own: the
	// case's is already cancelled by the time cleanup runs.
	t.Cleanup(func() {
		if err := table.RemoveAll(context.Background(), queuePrefix+"/"+queuefs.Jobs); err != nil {
			t.Errorf("RemoveAll(%s) = %v", queuePrefix+"/"+queuefs.Jobs, err)
		}
		if err := registry.Release(); err != nil {
			t.Errorf("Release() = %v", err)
		}
	})
	return table, mounts
}

// stackSetting is one end of the queue, or the reason there is no run.
func stackSetting(t *testing.T, name string) string {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		t.Skipf("set %s to run against a queue; `make up` starts one", name)
	}
	return value
}

// found reports whether any hit is at name inside the scope.
func found(hits []search.Hit, name string) bool {
	for _, hit := range hits {
		if hit.Path == name {
			return true
		}
	}
	return false
}

// paths is what a failure reports: where the hits were, without their scores.
func paths(hits []search.Hit) []string {
	at := make([]string, len(hits))
	for i, hit := range hits {
		at[i] = hit.Path
	}
	return at
}
