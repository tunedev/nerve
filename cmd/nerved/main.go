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
	// table itself comes from config.
	registry := vfs.NewRegistry()

	mounts := cfg.MountSpecs()
	table, err := registry.Table(mounts)
	if err != nil {
		fail(err)
	}

	fmt.Printf("nerved %s (%s)\n", version, runtime.Version())
	fmt.Printf("log level %s\n", cfg.LogLevel)
	for _, spec := range mounts {
		fmt.Printf("mount %s kind %s %+v\n", spec.Prefix, spec.Kind, table.Capabilities(spec.Prefix))
	}
	fmt.Printf("admin http://%s/config\n", cfg.AdminAddr)

	// Redaction happens once, at composition, so the surface never holds a
	// value it would have to remember to hide. What a mount setting reads back
	// as is the registry's answer: only the kind that registered knows which of
	// its settings is a credential, and what it does not declare is redacted.
	srv := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           admin.Handler(cfg.Effective(registry.Public)),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, srv); err != nil {
		fail(err)
	}
}

// serve runs the server until the context is cancelled, then drains it. The
// goroutine it starts ends when ListenAndServe returns, which Shutdown makes
// it do; the channel is buffered so it cannot outlive this function blocked on
// a send.
func serve(ctx context.Context, srv *http.Server) error {
	listening := make(chan error, 1)
	go func() { listening <- srv.ListenAndServe() }()

	select {
	case err := <-listening:
		return err
	case <-ctx.Done():
	}

	fmt.Println("nerved: draining")
	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(grace)
}

// fail reports and exits.
func fail(err error) {
	fmt.Fprintf(os.Stderr, "nerved: %v\n", err)
	os.Exit(1)
}
