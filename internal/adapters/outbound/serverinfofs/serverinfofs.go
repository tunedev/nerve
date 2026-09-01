// Package serverinfofs answers with what the process knows about itself: the
// version it was built at, the toolchain that built it, where it is running,
// how long it has been up, and what each of its mounts has been asked for.
//
// It is the effective-config surface's neighbour rather than its replacement.
// That surface answers over HTTP, for an operator with a browser; this one
// answers through the tree, for anything already reading the tree — an agent
// asking which build it is talking to should not have to learn a second
// protocol to find out.
package serverinfofs

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/synth"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The names this mount answers for.
const (
	Version   = "version"
	Toolchain = "go"
	PID       = "pid"
	Hostname  = "hostname"
	Started   = "started"
	Uptime    = "uptime"
	Traffic   = "traffic"
)

// heading names the columns the traffic entry answers with, so what a reader
// gets is self-describing rather than four numbers in an order it has to know.
const heading = "mount\tkind\tcalls\tfailed\n"

// unknownHost is what the hostname entry holds when the host will not say. It
// is a value rather than an error, because a mount that fails to come up over a
// hostname would take the rest of the tree with it.
const unknownHost = "unknown"

// New returns a mount describing this process, built at version, against the
// clock now reads, counting through traffic. The clock is a parameter because
// uptime is a function of it.
//
// The traffic entry is what makes per-mount accounting readable without a
// collector: the tally is kept in this process, and reading it is reading the
// tree. Reading it is itself a call on this mount, so what comes back includes
// the read that asked — the count is of what the process was asked for, and
// this was one of the asking.
func New(version string, now func() time.Time, traffic *vfs.Traffic) *synth.Tree {
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
		Traffic: {
			Content: func(context.Context) ([]byte, error) { return report(traffic.Tallies()), nil },
			Changed: traffic.Changed,
		},
	})
}

// report renders the tally as one heading and one line per mount, tab
// separated. Tabs rather than columns aligned to the widest value: the width of
// a column would then change with a counter, and a document that reflows as it
// counts is one nobody can diff.
func report(tallies []vfs.Tally) []byte {
	var out strings.Builder
	out.WriteString(heading)
	for _, tally := range tallies {
		fmt.Fprintf(&out, "%s\t%s\t%d\t%d\n", tally.Mount, tally.Kind, tally.Calls, tally.Failed)
	}
	return []byte(out.String())
}
