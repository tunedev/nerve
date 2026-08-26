// Command nerved is the composition root: the only place that knows every
// concrete type. It wires adapters to the ports the core declares.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/tunedev/nerve/internal/adapters/inbound/admin"
	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// The operator surface answers from memory, so these bound a slow or absent
// client rather than a slow answer. They are not settings: an operator who has
// to tune them has a different problem from the one this surface solves.
const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownGrace     = 5 * time.Second
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

	// Storage kinds register on this registry, here at composition and nowhere
	// else, because this is the only place that knows a concrete plugin. The
	// table itself comes from config, and so do the deadline every mount is
	// bounded by, the policy its transient failures are retried under and the
	// breaker that stops calling it when they do not stop: a mount may be
	// remote, and no caller of the tree can tell.
	retry := cfg.RetryPolicy()
	breaker := cfg.BreakerPolicy()
	registry := vfs.NewRegistry(cfg.CallTimeoutDuration(), retry, breaker)

	mounts := cfg.MountSpecs()
	table, err := registry.Table(mounts)
	if err != nil {
		fail(err)
	}

	fmt.Printf("nerved %s (%s)\n", version, runtime.Version())
	fmt.Printf("log level %s\n", cfg.LogLevel)
	fmt.Printf("call timeout %s\n", cfg.CallTimeoutDuration())
	fmt.Printf("retry %d attempts, backoff up to %s\n", retry.Attempts, retry.Backoff)
	fmt.Printf("breaker opens on %d transient failures in a row, cooldown %s\n", breaker.Threshold, breaker.Cooldown)
	for _, spec := range mounts {
		fmt.Printf("mount %s kind %s %+v\n", spec.Prefix, spec.Kind, table.Capabilities(spec.Prefix))
	}
	for _, path := range []string{"/config", "/livez", "/readyz"} {
		fmt.Printf("admin http://%s%s\n", cfg.AdminAddr, path)
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

	if err := serve(ctx, srv, health, cfg.DrainDelayDuration()); err != nil {
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
	fmt.Printf("nerved: draining, not ready for %s\n", drainDelay)
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
