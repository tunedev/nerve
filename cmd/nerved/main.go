// Command nerved is the composition root: the only place that knows every
// concrete type. It wires adapters to the ports the core declares.
package main

import (
	"fmt"
	"os"
	"runtime"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Printf("nerved %s (%s)\n", version, runtime.Version())
	fmt.Println("no surfaces wired yet; see docs/roadmap.md")
	os.Exit(0)
}
