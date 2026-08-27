// Package serverinfofs answers with what the process knows about itself: the
// version it was built at, the toolchain that built it, where it is running and
// how long it has been up.
//
// It is the effective-config surface's neighbour rather than its replacement.
// That surface answers over HTTP, for an operator with a browser; this one
// answers through the tree, for anything already reading the tree — an agent
// asking which build it is talking to should not have to learn a second
// protocol to find out.
package serverinfofs

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/synth"
)

// The names this mount answers for.
const (
	Version   = "version"
	Toolchain = "go"
	PID       = "pid"
	Hostname  = "hostname"
	Started   = "started"
	Uptime    = "uptime"
)

// unknownHost is what the hostname entry holds when the host will not say. It
// is a value rather than an error, because a mount that fails to come up over a
// hostname would take the rest of the tree with it.
const unknownHost = "unknown"

// New returns a mount describing this process, built at version, against the
// clock now reads. The clock is a parameter because uptime is a function of it.
func New(version string, now func() time.Time) *synth.Tree {
	started := now()

	host, err := os.Hostname()
	if err != nil {
		host = unknownHost
	}

	return synth.NewTree(map[string]synth.Entry{
		Version:   synth.Fixed(version),
		Toolchain: synth.Fixed(runtime.Version()),
		PID:       synth.Fixed(strconv.Itoa(os.Getpid())),
		Hostname:  synth.Fixed(host),
		Started:   synth.Fixed(started.Format(time.RFC3339Nano)),
		Uptime: {
			Content: func(context.Context) ([]byte, error) {
				return []byte(now().Sub(started).Round(time.Second).String() + "\n"), nil
			},
			Changed: now,
		},
	})
}
