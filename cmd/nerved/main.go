// Command nerved is the composition root: the only place that knows every
// concrete type. It wires adapters to the ports the core declares.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

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
		fmt.Fprintf(os.Stderr, "nerved: %v\n", err)
		os.Exit(1)
	}

	// Storage kinds register on this registry, here at composition and nowhere
	// else, because this is the only place that knows a concrete plugin. The
	// table itself comes from config.
	registry := vfs.NewRegistry()

	mounts := cfg.MountSpecs()
	table, err := registry.Table(mounts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerved: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("nerved %s (%s)\n", version, runtime.Version())
	fmt.Printf("log level %s\n", cfg.LogLevel)
	for _, spec := range mounts {
		fmt.Printf("mount %s kind %s %+v\n", spec.Prefix, spec.Kind, table.Capabilities(spec.Prefix))
	}
	fmt.Println("no surfaces wired yet; see docs/roadmap.md")
	os.Exit(0)
}
