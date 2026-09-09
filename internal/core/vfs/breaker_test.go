package vfs_test

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// steady opens after three transient failures in a row and stays open for
// longer than any test runs, so nothing here depends on the clock unless it
// says so.
var steady = stability.Breaker{Threshold: 3, Cooldown: time.Minute}

// brief opens on the first transient failure and reopens for a probe soon
// enough that a test can wait for it.
var brief = stability.Breaker{Threshold: 1, Cooldown: 20 * time.Millisecond}

// wobbly answers Read with whatever a test has set, counting the calls that
// reach it. It is how a test moves a far end between failing and answering.
type wobbly struct {
	stub
	vfs.NoAccessors
	err   error
	calls int
}

var _ vfs.FileSystem = &wobbly{}

func (w *wobbly) Read(context.Context, string) ([]byte, error) {
	w.calls++
	return nil, w.err
}

// read is one call through fsys, discarding the content a failing far end does
// not have.
func read(fsys vfs.FileSystem) error {
	_, err := fsys.Read(context.Background(), "/x")
	return err
}

// Enough transient failures in a row and the next call is not made at all.
// Hammering a dependency that is already failing makes it worse, and the caller
// waits out a timeout to learn what the breaker already knows.
func TestATransientFailureStreakOpensTheCircuit(t *testing.T) {
	plugin := &wobbly{err: errTransient}
	fsys := vfs.WithBreaker(plugin, steady)

	for range steady.Threshold {
		if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
			t.Fatalf("Read = %v, want the plugin's error", err)
		}
	}

	if err := read(fsys); !errors.Is(err, stability.ErrUnavailable) {
		t.Errorf("Read after %d transient failures = %v, want %v", steady.Threshold, err, stability.ErrUnavailable)
	}
	if plugin.calls != steady.Threshold {
		t.Errorf("the plugin saw %d calls, want %d: the open circuit is still calling it", plugin.calls, steady.Threshold)
	}
}

// An open circuit is fail-fast, which is what the retry above it needs to
// hear. Any other classification spends the remaining attempts on a dependency
// the breaker has just decided not to call.
func TestAnOpenCircuitIsFailFast(t *testing.T) {
	if got := stability.Classify(stability.ErrUnavailable); got != stability.FailFast {
		t.Errorf("Classify(ErrUnavailable) = %s, want %s", got, stability.FailFast)
	}
}

// A failure the far end is entitled to return is not the far end failing. A
// path that does not exist is a healthy no, and a mount that answers a hundred
// of them is a mount that is working.
func TestAnAnswerAboutTheWorldNeverOpensTheCircuit(t *testing.T) {
	plugin := &wobbly{err: fs.ErrNotExist}
	fsys := vfs.WithBreaker(plugin, steady)

	const calls = 100
	for range calls {
		if err := read(fsys); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Read = %v, want %v", err, fs.ErrNotExist)
		}
	}

	if plugin.calls != calls {
		t.Errorf("the plugin saw %d of %d calls, want all of them: an answer opened the circuit", plugin.calls, calls)
	}
}

// The count is of failures in a row. One answer in the middle of a bad patch
// is the far end talking, so the streak that opens the circuit has to be a
// streak.
func TestAnAnswerStartsTheCountOver(t *testing.T) {
	plugin := &wobbly{err: errTransient}
	fsys := vfs.WithBreaker(plugin, steady)

	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Fatalf("Read = %v, want the plugin's error", err)
	}
	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Fatalf("Read = %v, want the plugin's error", err)
	}

	plugin.err = nil
	if err := read(fsys); err != nil {
		t.Fatalf("Read = %v, want the plugin's answer", err)
	}

	plugin.err = errTransient
	for range steady.Threshold - 1 {
		if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
			t.Fatalf("Read = %v, want the plugin's error", err)
		}
	}

	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Errorf("Read = %v, want the plugin's error: two failures either side of an answer opened the circuit", err)
	}
}

// The circuit closes again, or nothing ever comes back. One call goes through
// after the cooldown to find out, and an answer resumes traffic.
func TestTheCircuitProbesAfterTheCooldownAndAnAnswerClosesIt(t *testing.T) {
	plugin := &wobbly{err: errTransient}
	fsys := vfs.WithBreaker(plugin, brief)

	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Fatalf("Read = %v, want the plugin's error", err)
	}
	if err := read(fsys); !errors.Is(err, stability.ErrUnavailable) {
		t.Fatalf("Read = %v, want %v", err, stability.ErrUnavailable)
	}

	time.Sleep(2 * brief.Cooldown)
	plugin.err = nil

	if err := read(fsys); err != nil {
		t.Fatalf("the probe after the cooldown = %v, want the plugin's answer", err)
	}
	if err := read(fsys); err != nil {
		t.Errorf("Read after a probe answered = %v, want the circuit closed", err)
	}
	if plugin.calls != 3 {
		t.Errorf("the plugin saw %d calls, want 3", plugin.calls)
	}
}

// A probe is one call, not the resumption of traffic. It reopens the circuit
// when it fails, and while it is in flight nothing follows it in.
func TestAFailedProbeReopensTheCircuit(t *testing.T) {
	plugin := &wobbly{err: errTransient}
	fsys := vfs.WithBreaker(plugin, brief)

	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Fatalf("Read = %v, want the plugin's error", err)
	}

	time.Sleep(2 * brief.Cooldown)

	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Fatalf("the probe after the cooldown = %v, want the plugin's error", err)
	}
	if err := read(fsys); !errors.Is(err, stability.ErrUnavailable) {
		t.Errorf("Read after a failed probe = %v, want %v", err, stability.ErrUnavailable)
	}
	if plugin.calls != 2 {
		t.Errorf("the plugin saw %d calls, want 2: one to open the circuit and one probe", plugin.calls)
	}
}

// One far end that has stopped talking has stopped talking to every verb, so
// the circuit is the mount's rather than the method's — the accessors' verbs
// included.
func TestAnOpenCircuitRefusesEveryVerb(t *testing.T) {
	for name, call := range verbs() {
		t.Run(name, func(t *testing.T) {
			plugin := &flaky{fail: 99, err: errTransient}
			fsys := vfs.WithBreaker(plugin, steady)

			for range steady.Threshold {
				if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
					t.Fatalf("Read = %v, want the plugin's error", err)
				}
			}
			opened := plugin.calls

			if err := call(context.Background(), fsys); !errors.Is(err, stability.ErrUnavailable) {
				t.Errorf("%s with the circuit open = %v, want %v", name, err, stability.ErrUnavailable)
			}
			if plugin.calls != opened {
				t.Errorf("%s reached the plugin with the circuit open", name)
			}
		})
	}
}

// An open is a call and is refused like one. What an open returns is not: a
// transfer that is running is a far end that is answering, and only the caller
// holding it knows what to do about it.
func TestAnOpenCircuitRefusesTheOpens(t *testing.T) {
	plugin := &flaky{fail: 99, err: errTransient}
	fsys := vfs.WithBreaker(plugin, steady)

	for range steady.Threshold {
		if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
			t.Fatalf("Read = %v, want the plugin's error", err)
		}
	}
	opened := plugin.calls

	if _, err := fsys.AsStreamer().OpenRead(context.Background(), "/big"); !errors.Is(err, stability.ErrUnavailable) {
		t.Errorf("OpenRead with the circuit open = %v, want %v", err, stability.ErrUnavailable)
	}
	if _, err := fsys.AsStreamer().OpenWrite(context.Background(), "/big"); !errors.Is(err, stability.ErrUnavailable) {
		t.Errorf("OpenWrite with the circuit open = %v, want %v", err, stability.ErrUnavailable)
	}
	if plugin.calls != opened {
		t.Errorf("an open reached the plugin with the circuit open")
	}
}

// The fallback a mount always has: it can still say what its paths mean.
// Capabilities is not I/O, so a caller that cannot read a path can still learn
// that reading it would consume it.
func TestAnOpenCircuitStillDescribesItself(t *testing.T) {
	plugin := &flaky{fail: 99, err: errTransient, caps: vfs.Caps{ReadDestructive: true}}
	fsys := vfs.WithBreaker(plugin, steady)

	for range steady.Threshold {
		if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
			t.Fatalf("Read = %v, want the plugin's error", err)
		}
	}

	if got := fsys.Capabilities("/jobs/next"); got != (vfs.Caps{ReadDestructive: true}) {
		t.Errorf("Capabilities() with the circuit open = %+v, want the plugin's", got)
	}
}

// The zero value is the breaker turned off, so a mount that was never given a
// policy is called exactly as it was before there was one.
func TestTheZeroPolicyNeverOpens(t *testing.T) {
	plugin := &wobbly{err: errTransient}
	fsys := vfs.WithBreaker(plugin, stability.Breaker{})

	const calls = 100
	for range calls {
		if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
			t.Fatalf("Read = %v, want the plugin's error", err)
		}
	}

	if plugin.calls != calls {
		t.Errorf("the plugin saw %d of %d calls, want all of them", plugin.calls, calls)
	}
}

// A circuit says nothing about what a plugin can do: a refused verb set is
// still refused, so the caller's nil guard keeps telling the truth.
func TestBreakingAPluginRefusesWhatItRefused(t *testing.T) {
	fsys := vfs.WithBreaker(plain{}, steady)

	if link := fsys.AsSymlinker(); link != nil {
		t.Errorf("AsSymlinker() = %v, want nil", link)
	}
	if stream := fsys.AsStreamer(); stream != nil {
		t.Errorf("AsStreamer() = %v, want nil", stream)
	}
	if cut := fsys.AsTruncater(); cut != nil {
		t.Errorf("AsTruncater() = %v, want nil", cut)
	}
}

// The breaker sits inside the retry, so it counts attempts rather than
// sequences and the retry stops the moment it opens. A retry loop that kept
// going would be the one thing the breaker exists to prevent.
func TestAnOpenCircuitStopsTheRetries(t *testing.T) {
	const threshold = 2

	plugin := &wobbly{err: errTransient}
	fsys := built(t, plugin, stability.Retry{Attempts: 5, Backoff: time.Millisecond}, stability.Breaker{Threshold: threshold, Cooldown: time.Minute})

	if err := read(fsys); !errors.Is(err, stability.ErrUnavailable) {
		t.Errorf("Read = %v, want %v", err, stability.ErrUnavailable)
	}
	if plugin.calls != threshold {
		t.Errorf("the plugin saw %d attempts, want %d: the retry kept going after the circuit opened", plugin.calls, threshold)
	}
}

// The acceptance criterion. A mount whose far end is down refuses its own
// subtree at once, and every other mount answers as if nothing had happened:
// one failing provider degrades a feature, not a path.
func TestABrokenMountLeavesTheRestOfTheTreeAnswering(t *testing.T) {
	const threshold = 2

	dead := &wobbly{err: errTransient}
	live := &wobbly{}

	registry := vfs.NewRegistry(time.Minute, stability.Retry{}, stability.Breaker{Threshold: threshold, Cooldown: time.Minute})
	register(t, registry, "dead", dead)
	register(t, registry, "live", live)

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/dead", Kind: "dead"}, {Prefix: "/live", Kind: "live"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}

	for range threshold {
		if _, err := table.Read(context.Background(), "/dead/x"); !errors.Is(err, stability.ErrTransient) {
			t.Fatalf("Read of the broken mount = %v, want the plugin's error", err)
		}
	}

	if _, err := table.Read(context.Background(), "/dead/x"); !errors.Is(err, stability.ErrUnavailable) {
		t.Errorf("Read of the broken mount = %v, want %v", err, stability.ErrUnavailable)
	}
	if dead.calls != threshold {
		t.Errorf("the broken mount saw %d calls, want %d", dead.calls, threshold)
	}

	for range threshold + 1 {
		if _, err := table.Read(context.Background(), "/live/x"); err != nil {
			t.Fatalf("Read of the working mount = %v, want its answer: the breakers are shared", err)
		}
	}
	if live.calls != threshold+1 {
		t.Errorf("the working mount saw %d of %d calls, want all of them", live.calls, threshold+1)
	}

	if table.Capabilities("/dead/x") != (vfs.Caps{}) {
		t.Error("the broken mount stopped describing itself")
	}
}

// built is one plugin behind the wrappers the registry puts on it, which is
// the only composition any caller of the tree ever holds.
func built(t *testing.T, plugin vfs.FileSystem, retry stability.Retry, breaker stability.Breaker) vfs.FileSystem {
	t.Helper()

	registry := vfs.NewRegistry(time.Minute, retry, breaker)
	register(t, registry, "plugin", plugin)

	fsys, err := registry.Build(vfs.MountSpec{Prefix: "/", Kind: "plugin"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return fsys
}

// register puts one already-built plugin under kind, which is how these tests
// name a fake in a registry that expects a factory.
func register(t *testing.T, registry *vfs.Registry, kind string, plugin vfs.FileSystem) {
	t.Helper()

	if err := registry.Register(kind, func(map[string]string) (vfs.FileSystem, error) { return plugin, nil }); err != nil {
		t.Fatalf("Register %q: %v", kind, err)
	}
}

// counted answers every Read with a transient failure, counting the calls that
// reach it from any number of goroutines at once.
type counted struct {
	stub
	vfs.NoAccessors
	calls atomic.Int64
}

func (c *counted) Read(context.Context, string) ([]byte, error) {
	c.calls.Add(1)
	return nil, errTransient
}

// Callers of a mount are concurrent, and the breaker is the one part of this
// stack that remembers anything between them. A burst arriving the moment the
// cooldown ends is one probe and the rest refused, not a burst against a far
// end that has not been asked whether it is back.
func TestABurstAfterTheCooldownIsOneProbe(t *testing.T) {
	const callers = 64

	plugin := &counted{}
	fsys := vfs.WithBreaker(plugin, brief)

	if err := read(fsys); !errors.Is(err, stability.ErrTransient) {
		t.Fatalf("Read = %v, want the plugin's error", err)
	}
	time.Sleep(2 * brief.Cooldown)

	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			_ = read(fsys)
		}()
	}
	wg.Wait()

	if got := plugin.calls.Load(); got != 2 {
		t.Errorf("the plugin saw %d calls, want 2: one to open the circuit and one probe", got)
	}
}
