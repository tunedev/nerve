package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// A queue mount is a flat key space, and a job key is a whole changed path. A
// mount that is a tree refuses the deep key it is given, and the report an
// observer makes is dropped by design — so the process would queue nothing,
// take nothing, say nothing and look healthy. This is the case that made that
// worth checking at boot rather than discovering in production.
func TestAQueueThatIsNotAFlatKeySpaceIsRefusedAtBoot(t *testing.T) {
	table := treeAt(t, "/queue", memfs.Kind, nil)

	_, err := consumer(table, nil, "/queue", "/resources", nil, nil, nil)
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("consumer(/queue over memfs) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "flat key space") {
		t.Errorf("consumer(/queue over memfs) = %v, want it to name what the mount is not", err)
	}
}

// A prefix that is not a mount cannot be a queue: the listener and the mount
// have to reach the same broker, and what the listener reaches it with is the
// mount's own config.
func TestAnIngestQueueThatNamesNoMountIsRefusedAtBoot(t *testing.T) {
	table := treeAt(t, "/queue", memfs.Kind, nil)

	_, err := consumer(table, nil, "/nothing/here", "/resources", nil, nil, nil)
	if !errors.Is(err, fs.ErrInvalid) && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("consumer(/nothing/here) = %v, want a refusal", err)
	}
}

// A build that names no queue takes uploads and derives nothing from them. The
// tree the store writes through is the table itself, so nothing is reported and
// nothing is queued; the worker does not exist, and stopping one that was never
// started is not an error.
func TestABuildWithNoQueueRunsNoWorkerAndObservesNothing(t *testing.T) {
	table := treeAt(t, "/resources", memfs.Kind, nil)

	if got := uploads(table, ""); got != vfs.FileSystem(table) {
		t.Errorf("uploads(table, %q) wrapped the tree, want it left alone", "")
	}
	if got := uploads(table, "/queue"); got == vfs.FileSystem(table) {
		t.Errorf("uploads(table, %q) left the tree alone, want it observed", "/queue")
	}

	consuming, err := consumer(table, nil, "", "/resources", nil, nil, nil)
	if err != nil {
		t.Fatalf("consumer(no queue) = %v", err)
	}
	consuming.start(t.Context())
	if err := consuming.stop(); err != nil {
		t.Errorf("stop() = %v, want nothing to stop", err)
	}
}

// The ledger stands under a root of its own. A build that names none holds
// none, which config only allows when the build ingests nothing.
func TestTheLedgerIsBuiltOnlyWhereARootNamesOne(t *testing.T) {
	table := treeAt(t, "/ingest", memfs.Kind, nil)

	none, err := indexingLedger(table, "")
	if err != nil || none != nil {
		t.Fatalf("indexingLedger(table, %q) = %v, %v, want no ledger and no error", "", none, err)
	}
	held, err := indexingLedger(table, "/ingest")
	if err != nil || held == nil {
		t.Fatalf("indexingLedger(table, %q) = %v, %v, want a ledger", "/ingest", held, err)
	}
}

// The pattern a worker subscribes with is the resources subtree in the tree's
// own vocabulary. A key is the changed path with its leading separator gone,
// which is what the emitter publishes and what the worker is told.
func TestTheSubscriptionIsTheResourcesSubtree(t *testing.T) {
	if got, want := subtree("/resources"), "resources/#"; got != want {
		t.Errorf("subtree(%q) = %q, want %q", "/resources", got, want)
	}
}

// An index whose corpus cannot hold a document key would fail every document at
// the index stage of a process that booted looking healthy, and a prefix that is
// not a mount is no index at all. Both are refused before anything is ingested.
func TestAnIngestIndexThatCannotHoldADocumentIsRefusedAtBoot(t *testing.T) {
	mounts := []vfs.MountSpec{{Prefix: "/index", Kind: memfs.Kind}}
	table := treeAt(t, "/index", memfs.Kind, nil)

	if _, err := indexers(table, mounts, "/index", nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("indexers(/index over memfs) = %v, want fs.ErrInvalid", err)
	}
	if _, err := indexers(table, mounts, "/nothing", nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("indexers(/nothing) = %v, want fs.ErrNotExist", err)
	}
	none, err := indexers(table, mounts, "", nil)
	if err != nil || none != nil {
		t.Errorf("indexers(no index) = %v, %v, want nothing and no error", none, err)
	}
}

// An ingest queue with nothing to ingest from, or nowhere to record what it
// did, is a process that would claim jobs and answer nobody. Both are boot
// failures rather than a worker running against half a wiring.
func TestInvalidIngestConfigFailsAtBoot(t *testing.T) {
	for what, edit := range map[string]func(*config.Config){
		"a queue with no resources tree":    func(c *config.Config) { c.ResourcesRoot = "" },
		"a queue with no ledger root":       func(c *config.Config) { c.IngestRoot = "" },
		"a queue that is not a path":        func(c *config.Config) { c.IngestQueue = "queue" },
		"a ledger root that is not a path":  func(c *config.Config) { c.IngestRoot = "ingest" },
		"an index that is not a path":       func(c *config.Config) { c.IngestIndex = "vectors" },
		"an index with no queue feeding it": func(c *config.Config) { c.IngestQueue = ""; c.IngestIndex = "/vectors" },
	} {
		t.Run(what, func(t *testing.T) {
			cfg := ingestingConfig(t)
			edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate() = nil, want a boot failure for %s", what)
			}
		})
	}
}

// ingestingConfig is a config that boots an ingesting build, which each case
// above breaks one way.
func ingestingConfig(t *testing.T) config.Config {
	t.Helper()

	cfg, err := config.Load(config.Sources{Lookup: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	cfg.ResourcesRoot = "/resources"
	cfg.DownloadKeyFile = "/dev/null"
	cfg.IngestQueue = "/queue"
	cfg.IngestRoot = "/ingest"
	cfg.IngestIndex = "/vectors"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the config every case starts from does not boot: %v", err)
	}
	return cfg
}

// treeAt resolves the mount table one line of mount config produces, through
// the same registration the composition root does.
func treeAt(t *testing.T, prefix, kind string, settings map[string]string) *vfs.MountTable {
	t.Helper()

	registry := vfs.NewRegistry(time.Minute, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", nil, vfs.NewTraffic(time.Now), nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}
	table, err := registry.Table([]vfs.MountSpec{{Prefix: prefix, Kind: kind, Settings: settings}})
	if err != nil {
		t.Fatalf("Table(%s) = %v", prefix, err)
	}
	t.Cleanup(func() {
		if err := registry.Release(); err != nil {
			t.Errorf("Release() = %v", err)
		}
	})
	return table
}
