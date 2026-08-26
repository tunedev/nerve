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

	fmt.Printf("nerved %s (%s)\n", version, runtime.Version())
	fmt.Printf("log level %s\n", cfg.LogLevel)
	fmt.Println("no surfaces wired yet; see docs/roadmap.md")
	os.Exit(0)
}
