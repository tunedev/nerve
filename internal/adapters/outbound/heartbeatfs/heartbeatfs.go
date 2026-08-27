// Package heartbeatfs answers with the pulse of the process. It holds two
// entries: the time of the most recent beat, and how many beats there have been
// since the mount came up. Nothing writes them and nothing stores them — they
// are a clock read at the moment somebody asks.
//
// It is what a watchdog reads. A mount whose count stops rising is a process
// that stopped, and reading it twice a beat apart tells the two conditions
// apart without the process having to publish anything.
package heartbeatfs

import (
	"context"
	"strconv"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/synth"
)

// Beat and Count are the two names this mount answers for.
const (
	Beat  = "beat"
	Count = "count"
)

// New returns a mount beating once per interval, against the clock now reads.
// The clock is a parameter because the whole of what this plugin says is a
// function of time, and a test that cannot move time cannot say anything about
// it.
func New(interval time.Duration, now func() time.Time) *synth.Tree {
	started := now()

	beats := func() int64 { return int64(now().Sub(started) / interval) }
	latest := func() time.Time { return started.Add(time.Duration(beats()) * interval) }

	return synth.NewTree(map[string]synth.Entry{
		Beat: {
			Content: func(context.Context) ([]byte, error) {
				return []byte(latest().Format(time.RFC3339Nano) + "\n"), nil
			},
			Changed: latest,
		},
		Count: {
			Content: func(context.Context) ([]byte, error) {
				return []byte(strconv.FormatInt(beats(), 10) + "\n"), nil
			},
			Changed: latest,
		},
	})
}
