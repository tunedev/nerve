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
	// The keyspace every kvfs mount is a part of is built before the table that
	// mounts over it, because a store two mounts share is the composition
	// root's to own rather than a mount's to build.
	retry := cfg.RetryPolicy()
	breaker := cfg.BreakerPolicy()
	store, err := keyspace(cfg.KVStoreURL)
	if err != nil {
		fail(err)
	}
	registry := vfs.NewRegistry(cfg.CallTimeoutDuration(), retry, breaker)
	registry.Record(signals.Recorder)
	registry.ReadAhead(cfg.ReadaheadBytes())
	if err := registerKinds(registry, version, store); err != nil {
		fail(err)
	}

	mounts := cfg.MountSpecs()
	table, err := registry.Table(mounts)
	if err != nil {
		fail(err)
	}

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

	// The formats this build can ingest register here for the reason storage
	// kinds do: this is the only place that knows a concrete processor. What a
	// resource is read by is the media type it declared, so which types the
	// build has is an operations answer and goes out with the rest of the
	// banner.
	processors := ingest.NewRegistry()
	if err := registerProcessors(processors); err != nil {
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
		"readahead-window", cfg.ReadaheadBytes(),
		"telemetry-endpoint", cfg.TelemetryEndpoint,
		"kv-store-url", config.Redact(cfg.KVStoreURL))
	for _, spec := range mounts {
		slog.Info("mount", "prefix", spec.Prefix, "kind", spec.Kind, "caps", table.Capabilities(spec.Prefix))
	}
	for _, spec := range libraries.Specs() {
		slog.Info("library", "kind", spec.Kind.Name, "root", spec.Root, "file", spec.Kind.Filename)
	}
	slog.Info("ingest", "formats", processors.MediaTypes())
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
		Handler:           admin.Handler(cfg.Effective(registry.Public), health),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	served := serve(ctx, srv, health, cfg.DrainDelayDuration())

	// The mounts are released after the server has stopped answering, so
	// nothing is still mid-verb on one. A plugin holding a database connection
	// or a broker channel gives it up here; before this, the only thing that
	// ever closed one was the process ending.
	//
	// The flush comes last, after everything that could record has stopped and
	// before the process is gone, so what the last requests recorded is
	// exported rather than dropped on exit. It runs on its own context: the one
	// that ended is why we are here.
	flush, cancel := context.WithTimeout(context.Background(), flushGrace)
	defer cancel()
	if err := errors.Join(served, registry.Release(), signals.Shutdown(flush)); err != nil {
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
