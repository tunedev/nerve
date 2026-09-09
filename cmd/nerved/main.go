// Command nerved is the composition root: the only place that knows every
// concrete type. It wires adapters to the ports the core declares.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/tunedev/nerve/internal/adapters/inbound/admin"
	"github.com/tunedev/nerve/internal/adapters/outbound/telemetry"
	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/library"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// service is the name this build reports itself to a collector under.
const service = "nerved"

// The operator surface answers from memory, so these bound a slow or absent
// client rather than a slow answer. They are not settings: an operator who has
// to tune them has a different problem from the one this surface solves.
const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownGrace     = 5 * time.Second

	// flushGrace bounds the last export. A collector that has stopped
	// answering must not hold the process open, and telemetry nobody can send
	// is worth less than an exit.
	flushGrace = 5 * time.Second
)

func main() {
	cfg, err := config.Load(config.Sources{
		File:   os.Getenv("NERVE_CONFIG"),
		Lookup: os.LookupEnv,
		Args:   os.Args[1:],
	})
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fail(err)
	}

	// Telemetry is built before anything it measures, because it owns the
	// logger everything after this line writes through. An empty endpoint
	// exports nothing and still logs, which is the laptop with no collector
	// running.
	signals, err := telemetry.New(context.Background(), telemetry.Options{
		Endpoint: cfg.TelemetryEndpoint,
		Service:  service,
		Version:  version,
		Level:    cfg.LogLevelValue(),
	})
	if err != nil {
		fail(err)
	}
	slog.SetDefault(signals.Logger)

	// Storage kinds register on this registry, here at composition and nowhere
	// else, because this is the only place that knows a concrete plugin. The
	// table itself comes from config, and so do the deadline every mount is
	// bounded by, the policy its transient failures are retried under and the
	// breaker that stops calling it when they do not stop, and how far in front
	// of a caller its streaming reads are fetched: a mount may be remote, and no
	// caller of the tree can tell.
	//
	// The keyspace every kvfs mount is a part of and the corpus every vectorfs
	// mount keeps its documents in are built before the table that mounts over
	// them, because a store two mounts share is the composition root's to own
	// rather than a mount's to build. Every graphfs mount keeps its blob in
	// that same corpus, under a namespace of its own.
	//
	// The tally counts what every mount is asked for and is read back through
	// the tree, so per-mount accounting is answerable on a laptop with no
	// collector running. It is a second recorder rather than a second wrapper:
	// the report a call already makes is what both of them read.
	traffic := vfs.NewTraffic(time.Now)
	retry := cfg.RetryPolicy()
	breaker := cfg.BreakerPolicy()
	store, err := keyspace(cfg.KVStoreURL)
	if err != nil {
		fail(err)
	}
	documents, err := corpus(cfg.DocStoreURL)
	if err != nil {
		fail(err)
	}

	// The model endpoints this build has register here for the reason storage
	// kinds do: this is the only place that knows a concrete provider. The
	// chain comes from config in the order it is asked, so putting a hosted
	// model in front of the laptop's is moving a line, and nothing downstream
	// of the gateway ever holds a client.
	//
	// Its deadline is its own rather than the one every mount is bounded by,
	// because a model is slow in a way a filesystem is not. The retry and
	// breaker policies are shared: a transient failure and a far end that has
	// stopped answering mean the same thing on either side of the process.
	//
	// The chain is built before the mount table because a mount may be over a
	// model: an index stores the vector of every document written to it and a
	// graph stores what every text named, and the routed Embedder and
	// EntityExtractor are what make those. Nothing points back the other way,
	// so this is the whole of the ordering.
	providers := llm.NewRegistry(cfg.ProviderTimeoutDuration(), retry, breaker)
	if err := registerProviders(providers); err != nil {
		fail(err)
	}

	gateway, err := providers.Gateway(cfg.ProviderSpecs())
	if err != nil {
		fail(err)
	}

	fused := &grown{}
	registry := vfs.NewRegistry(cfg.CallTimeoutDuration(), retry, breaker)
	registry.Record(vfs.Recorders(signals.Recorder, traffic))
	registry.ReadAhead(cfg.ReadaheadBytes())
	if err := registerKinds(registry, version, store, traffic, gateway.AsEmbedder(), documents,
		gateway.AsEntityExtractor(), graphStore(documents), fused.tree); err != nil {
		fail(err)
	}

	mounts := cfg.MountSpecs()
	table, err := registry.Table(mounts)
	if err != nil {
		fail(err)
	}
	fused.table = table

	// The libraries this process serves come from the same document: a kind is
	// a name and the file that makes a directory one of its documents, and a
	// root is where that kind's mount was put. Nothing here names a kind, which
	// is what makes a third sort of document a line of config rather than a
	// type. Which matcher ranks them is composition too — term overlap on the
	// laptop, an embedding matcher over a provider once there is one.
	libraries, err := library.NewCatalog(table, termmatch.New(), cfg.LibrarySpecs())
	if err != nil {
		fail(err)
	}

	// The resources and the scratch state of every workspace stand on the same
	// tree the mounts present, each under a root this process was configured
	// with. It is one decision named twice rather than two: an area of the
	// tree, spelled the way a library's root is spelled, derived from on every
	// call by the scope that owns it. A build that names neither holds neither.
	//
	// The key a download grant is signed with comes from a file the config
	// names, never from a setting the config carries. A secret has its own
	// lifecycle — rotated without a config change, stored where the document is
	// not — and keeping no layer able to hold it is what keeps it out of the
	// banner below and off the operator surface.
	//
	// The resources tree is the one thing here that is written through an
	// observer. Every write a resource makes is reported to the queue named in
	// config, which is how ingestion is triggered by the upload rather than by
	// the uploader remembering to ask; a build that names no queue writes
	// through the plain table and derives nothing.
	resources, err := resourceStore(uploads(table, cfg.IngestQueue), cfg.ResourcesRoot, cfg.DownloadKeyFile)
	if err != nil {
		fail(err)
	}
	scratch, err := scratchSpace(table, cfg.ScratchRoot)
	if err != nil {
		fail(err)
	}

	// The formats this build can ingest register here for the reason storage
	// kinds do: this is the only place that knows a concrete processor. What a
	// resource is read by is the media type it declared, so which types the
	// build has is an operations answer and goes out with the rest of the
	// banner.
	processors := ingest.NewRegistry()
	if err := registerProcessors(processors); err != nil {
		fail(err)
	}

	// The consuming end of ingestion: a ledger to record where a run got to, a
	// pipeline over the stages this build has, and a worker claiming the jobs
	// the observed tree queued. It is built here and started below, after the
	// banner, because a process that failed to compose should say so before it
	// starts anything.
	//
	// The ledger stands under a root of its own rather than beside the resource
	// it is about: a record written into the observed tree would queue an
	// ingestion of itself.
	ledger, err := indexingLedger(table, cfg.IngestRoot)
	if err != nil {
		fail(err)
	}
	consuming, err := consumer(table, mounts, cfg.IngestQueue, cfg.ResourcesRoot, resources, ledger, processors)
	if err != nil {
		fail(err)
	}

	// The banner goes through the logger rather than to stdout, so what a
	// process says about itself at boot reaches the same place as what it says
	// afterwards. An operator reading a collector should not have to also read
	// a terminal to find out what the process booted with.
	slog.Info("nerved starting",
		"version", version,
		"go", runtime.Version(),
		"log-level", cfg.LogLevel,
		"call-timeout", cfg.CallTimeoutDuration(),
		"retry-attempts", retry.Attempts,
		"retry-backoff", retry.Backoff,
		"breaker-threshold", breaker.Threshold,
		"breaker-cooldown", breaker.Cooldown,
		"provider-timeout", cfg.ProviderTimeoutDuration(),
		"readahead-window", cfg.ReadaheadBytes(),
		"telemetry-endpoint", cfg.TelemetryEndpoint,
		"kv-store-url", config.Redact(cfg.KVStoreURL),
		"doc-store-url", config.Redact(cfg.DocStoreURL))
	for _, spec := range mounts {
		slog.Info("mount", "prefix", spec.Prefix, "kind", spec.Kind, "caps", table.Capabilities(spec.Prefix))
	}
	for _, spec := range libraries.Specs() {
		slog.Info("library", "kind", spec.Kind.Name, "root", spec.Root, "file", spec.Kind.Filename)
	}
	if resources != nil {
		slog.Info("resources", "root", cfg.ResourcesRoot, "download-key-file", cfg.DownloadKeyFile)
	}
	if scratch != nil {
		slog.Info("scratch", "root", cfg.ScratchRoot)
	}
	slog.Info("ingest", "formats", processors.MediaTypes())
	consuming.describe(cfg.IngestQueue, cfg.IngestRoot)
	slog.Info("gateway", "providers", gateway.Providers(), "serves", gateway.Capabilities())
	for _, path := range []string{"/config", "/livez", "/readyz"} {
		slog.Info("admin", "url", fmt.Sprintf("http://%s%s", cfg.AdminAddr, path))
	}

	// Health is the process's own answer about itself, so the probes stay
	// answerable when Postgres is not. The composition root holds it because
	// it owns the drain.
	health := &admin.Health{}

	// Redaction happens once, at composition, so the surface never holds a
	// value it would have to remember to hide. What a mount setting reads back
	// as is the registry's answer: only the kind that registered knows which of
	// its settings is a credential, and what it does not declare is redacted.
	srv := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           admin.Handler(cfg.Effective(registry.Public, providers.Public), health),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The worker runs beside the server for as long as the process does, on the
	// context signals cancel. Nothing else stops it: a queue with nobody
	// publishing is a worker waiting, which is what it should be doing.
	consuming.start(ctx)

	served := serve(ctx, srv, health, cfg.DrainDelayDuration())

	// The mounts are released after the server has stopped answering and after
	// the worker has finished the job it was on, so nothing is still mid-verb on
	// one. A plugin holding a database connection or a broker channel gives it
	// up here; before this, the only thing that ever closed one was the process
	// ending.
	//
	// The flush comes last, after everything that could record has stopped and
	// before the process is gone, so what the last requests recorded is
	// exported rather than dropped on exit. It runs on its own context: the one
	// that ended is why we are here.
	flush, cancel := context.WithTimeout(context.Background(), flushGrace)
	defer cancel()
	if err := errors.Join(served, consuming.stop(), registry.Release(), signals.Shutdown(flush)); err != nil {
		fail(err)
	}
}

// serve runs the server until the context is cancelled, then drains it. The
// goroutine it starts ends when ListenAndServe returns, which Shutdown makes
// it do; the channel is buffered so it cannot outlive this function blocked on
// a send.
//
// Readiness fails the moment the drain begins and liveness keeps passing
// through it, so whatever routes traffic here stops sending before the server
// stops answering, and whatever restarts crashed processes leaves this one
// alone. The listener stays open for drainDelay so that refusal is observable:
// closed at once, the only signal is a refused connection.
func serve(ctx context.Context, srv *http.Server, health *admin.Health, drainDelay time.Duration) error {
	listening := make(chan error, 1)
	go func() { listening <- srv.ListenAndServe() }()

	select {
	case err := <-listening:
		return err
	case <-ctx.Done():
	}

	health.Drain()
	slog.Info("nerved draining, not ready", "for", drainDelay)
	time.Sleep(drainDelay)

	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(grace)
}

// fail reports and exits.
func fail(err error) {
	fmt.Fprintf(os.Stderr, "nerved: %v\n", err)
	os.Exit(1)
}
