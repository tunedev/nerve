package vfs

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tally is what one mount has been asked for since the process came up: how
// many calls arrived and how many of them failed.
//
// It is a count and not a rate. A rate needs a window, a window needs a
// retention policy, and both of those are the collector's job — this is the
// number an operator with no collector can still read.
type Tally struct {
	// Mount is the prefix the plugin answers for.
	Mount string
	// Kind is the registered name of what answers there.
	Kind string
	// Calls is how many calls reached the mount, including any still running.
	Calls uint64
	// Failed is how many of them came back with an error.
	Failed uint64
}

// Traffic counts calls per mount and is a Recorder, so the accounting is one
// more thing the report a call already makes is read for rather than a second
// wrapper on the way to every plugin.
//
// It exists beside the telemetry recorder rather than inside it because the
// two answer to different readers. A histogram is worth having and is only
// readable where the collector is; this is readable from the tree, by anything
// already reading the tree and by an operator on a laptop with nothing running.
// A build exporting nothing still counts.
//
// Counting is per mount rather than per verb because the question is which
// mount the process is spending itself on. Verb, path and duration are already
// in the span, and a per-verb tally read as text is a table nobody scans.
//
// The mount set is not known when this is built: mounts arrive as calls do, so
// the map has to be written to on a path every call takes. It is written the
// way MountTable is — replaced wholesale under a mutex nothing reading it
// takes, so a call loads one pointer and waits for nothing. Writing happens
// once per mount and reading happens once per call, which is the same
// read-mostly shape the table measured and the reason not to put a lock on it.
type Traffic struct {
	now  func() time.Time
	last atomic.Int64

	// adding serialises first sight of a mount against another. Calls counting
	// through a mount already seen never take it.
	adding sync.Mutex
	mounts atomic.Pointer[map[string]*counter]
}

// counter is one mount's running total. Kind is fixed per mount and written
// once, when the mount is first seen.
type counter struct {
	kind   string
	calls  atomic.Uint64
	failed atomic.Uint64
}

// NewTraffic returns a tally that has counted nothing, against the clock now
// reads. The clock is a parameter because when the last call landed is a
// function of it, and that is what the entry reading this reports as its
// modification time.
func NewTraffic(now func() time.Time) *Traffic {
	traffic := &Traffic{now: now}
	traffic.last.Store(now().UnixNano())
	traffic.mounts.Store(&map[string]*counter{})
	return traffic
}

// Begin counts the call and hands back a function that counts its failure. The
// caller's context is returned untouched: a tally carries nothing into the call
// it is counting.
//
// The call is counted here rather than when it ends, so a mount whose calls
// hang reads as a mount being called rather than as one nobody is using.
func (t *Traffic) Begin(ctx context.Context, call Call) (context.Context, Done) {
	count := t.counterFor(call.Mount, call.Kind)
	count.calls.Add(1)

	return ctx, func(err error) {
		if err != nil {
			count.failed.Add(1)
		}
		t.last.Store(t.now().UnixNano())
	}
}

// Tallies is what every mount has been asked for, sorted by mount, because the
// tally is read as a document and a document that reorders itself between reads
// is one nobody can diff.
func (t *Traffic) Tallies() []Tally {
	counted := t.snapshot()

	tallies := make([]Tally, 0, len(counted))
	for mount, count := range counted {
		tallies = append(tallies, Tally{
			Mount:  mount,
			Kind:   count.kind,
			Calls:  count.calls.Load(),
			Failed: count.failed.Load(),
		})
	}

	slices.SortFunc(tallies, func(a, b Tally) int { return strings.Compare(a.Mount, b.Mount) })
	return tallies
}

// Changed is when the last call ended, or when the tally was made if none has.
func (t *Traffic) Changed() time.Time { return time.Unix(0, t.last.Load()) }

// counterFor is the running total for mount, made on first sight of it. The
// map is replaced rather than written into, so nothing reading it needs a lock.
func (t *Traffic) counterFor(mount, kind string) *counter {
	if count, seen := t.snapshot()[mount]; seen {
		return count
	}

	t.adding.Lock()
	defer t.adding.Unlock()

	current := t.snapshot()
	if count, seen := current[mount]; seen {
		return count
	}

	count := &counter{kind: kind}
	next := maps.Clone(current)
	next[mount] = count
	t.mounts.Store(&next)
	return count
}

// snapshot is the counters as they stand, safe to read without a lock because
// nothing ever writes into a map that has been stored.
func (t *Traffic) snapshot() map[string]*counter { return *t.mounts.Load() }
