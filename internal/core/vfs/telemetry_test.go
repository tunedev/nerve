package vfs_test

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// recorded is one call as the recorder saw it: what the core said about it, and
// what it returned.
type recorded struct {
	call vfs.Call
	err  error
	// ended reports whether Done was called, which is what separates a call
	// that was measured from one that was only started.
	ended bool
}

// spy is a Recorder that keeps what it was told. It also puts a value on the
// context it hands back, so a test can ask whether the context a recorder
// returns is the one the plugin actually sees — the whole of how a span reaches
// a driver.
type spy struct {
	mu    sync.Mutex
	calls []*recorded
}

type spyKey struct{}

func (s *spy) Begin(ctx context.Context, call vfs.Call) (context.Context, vfs.Done) {
	entry := &recorded{call: call}

	s.mu.Lock()
	s.calls = append(s.calls, entry)
	s.mu.Unlock()

	return context.WithValue(ctx, spyKey{}, entry), func(err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		entry.err, entry.ended = err, true
	}
}

func (s *spy) seen() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]recorded, len(s.calls))
	for i, entry := range s.calls {
		out[i] = *entry
	}
	return out
}

// The coverage half: the verb list a call can leave through is the port's, and
// every one of them is reported. A verb added to the port and forwarded without
// a report fails here, which is the only warning anyone gets — no linter reads
// a call for whether it is measured.
func TestEveryVerbThatSendsIsRecorded(t *testing.T) {
	t.Parallel()

	sending, _ := portVerbs()

	for _, name := range sending {
		send := outbound()[name]
		if send == nil {
			t.Fatalf("%s sends but the coverage table does not drive it", name)
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := &spy{}
			fsys := vfs.WithTelemetry(vfs.WithTimeout(deaf{}, 10*time.Millisecond), rec, "/mnt", "deaf")

			_ = send(context.Background(), fsys)

			seen := rec.seen()
			if len(seen) != 1 {
				t.Fatalf("%s reported %d calls, want 1", name, len(seen))
			}
			if seen[0].call.Verb != name {
				t.Errorf("verb reported = %q, want %q", seen[0].call.Verb, name)
			}
			if !seen[0].ended {
				t.Errorf("%s was started and never ended; it is timed from nothing", name)
			}
			if !errors.Is(seen[0].err, context.DeadlineExceeded) {
				t.Errorf("%s reported err = %v, want the %v the call returned", name, seen[0].err, context.DeadlineExceeded)
			}
		})
	}
}

// answering is a plugin that answers every verb at once, so a test can ask what
// a call that worked reports.
type answering struct{ deaf }

func (answering) Read(context.Context, string) ([]byte, error) { return []byte("x"), nil }
func (answering) Write(context.Context, string, []byte) error  { return nil }
func (answering) Rename(context.Context, string, string) error { return nil }

func TestReportNamesTheMountTheKindAndThePath(t *testing.T) {
	t.Parallel()

	rec := &spy{}
	fsys := vfs.WithTelemetry(answering{}, rec, "/data", "memfs")

	if _, err := fsys.Read(context.Background(), "/notes/one"); err != nil {
		t.Fatalf("Read: %v", err)
	}

	seen := rec.seen()
	if len(seen) != 1 {
		t.Fatalf("reported %d calls, want 1", len(seen))
	}

	want := vfs.Call{Mount: "/data", Kind: "memfs", Verb: "Read", Path: "/notes/one"}
	if seen[0].call != want {
		t.Errorf("call = %+v, want %+v", seen[0].call, want)
	}
	if seen[0].err != nil {
		t.Errorf("a call that worked reported err = %v, want nil", seen[0].err)
	}
}

// A verb naming two paths reports the one it starts from. Reporting neither
// leaves a slow rename with no path at all, and reporting both makes one call
// two.
func TestATwoPathVerbReportsWhereItStarted(t *testing.T) {
	t.Parallel()

	rec := &spy{}
	fsys := vfs.WithTelemetry(answering{}, rec, "/data", "memfs")

	if err := fsys.Rename(context.Background(), "/from", "/to"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if path := rec.seen()[0].call.Path; path != "/from" {
		t.Errorf("Rename reported path %q, want %q", path, "/from")
	}
}

// The context a recorder returns is the one the plugin runs on. A recorder that
// opened a span and handed the context back to nobody would trace one call and
// lose everything the call went on to do.
func TestThePluginRunsOnTheContextTheRecorderReturned(t *testing.T) {
	t.Parallel()

	rec := &spy{}
	seen := make(chan bool, 1)
	fsys := vfs.WithTelemetry(carrying{seen: seen}, rec, "/data", "memfs")

	if err := fsys.Write(context.Background(), "/x", nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !<-seen {
		t.Error("the plugin ran on the caller's context; what the recorder put on its own reaches nothing")
	}
}

// carrying reports whether the context it was called with carries what the spy
// put there.
type carrying struct {
	deaf
	seen chan bool
}

func (c carrying) Write(ctx context.Context, _ string, _ []byte) error {
	c.seen <- ctx.Value(spyKey{}) != nil
	return nil
}

// Telemetry says nothing about what a plugin can do. A plugin with no links is
// still a plugin with no links once it is measured, so the caller's nil guard
// keeps telling the truth.
func TestTelemetryDoesNotInventVerbSets(t *testing.T) {
	t.Parallel()

	fsys := vfs.WithTelemetry(plain{}, &spy{}, "/data", "memfs")

	if fsys.AsSymlinker() != nil {
		t.Error("AsSymlinker of a plugin with no links is not nil")
	}
	if fsys.AsStreamer() != nil {
		t.Error("AsStreamer of a plugin that does not stream is not nil")
	}
	if fsys.AsTruncater() != nil {
		t.Error("AsTruncater of a plugin that does not truncate is not nil")
	}
}

// Capabilities is not a call and there is nothing to report about it. A mount
// answers it from the shape it already knows.
func TestCapabilitiesIsNotReported(t *testing.T) {
	t.Parallel()

	rec := &spy{}
	vfs.WithTelemetry(answering{}, rec, "/data", "memfs").Capabilities("/x")

	if seen := rec.seen(); len(seen) != 0 {
		t.Errorf("Capabilities reported %d calls, want none", len(seen))
	}
}

// Measurement has to survive dispatch, not merely exist. What a caller holds is
// the mount table, and a report has to name the mount the call landed on and
// the path in that mount's own vocabulary.
func TestACallThroughTheTreeIsReportedAgainstItsMount(t *testing.T) {
	t.Parallel()

	rec := &spy{}
	registry := vfs.NewRegistry(time.Second, vfs.Retry{}, vfs.Breaker{Threshold: 2, Cooldown: time.Minute})
	registry.Record(rec)
	if err := registry.Register("answering", func(map[string]string) (vfs.FileSystem, error) {
		return answering{}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/data", Kind: "answering"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}

	if _, err := table.Read(context.Background(), "/data/notes/one"); err != nil {
		t.Fatalf("Read: %v", err)
	}

	want := vfs.Call{Mount: "/data", Kind: "answering", Verb: "Read", Path: "/notes/one"}
	if got := rec.seen()[0].call; got != want {
		t.Errorf("call through the tree = %+v, want %+v", got, want)
	}
}

// A registry nobody told where to send reports still builds a working tree. The
// wrapper is on either way, so the path a build with telemetry takes is the
// path every build takes.
func TestARegistryWithNoRecorderStillBuilds(t *testing.T) {
	t.Parallel()

	registry := vfs.NewRegistry(time.Second, vfs.Retry{}, vfs.Breaker{Threshold: 2, Cooldown: time.Minute})
	if err := registry.Register("answering", func(map[string]string) (vfs.FileSystem, error) {
		return answering{}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "answering"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if _, err := table.Read(context.Background(), "/x"); err != nil {
		t.Fatalf("Read through an unrecorded tree: %v", err)
	}
}

// NopRecorder hands back the caller's own context. A recorder that replaced it
// with one of its own would put a value on the call that nothing outside the
// recorder can explain.
func TestNopRecorderChangesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	got, done := vfs.NopRecorder{}.Begin(ctx, vfs.Call{})

	if got != ctx {
		t.Error("NopRecorder returned a context other than the caller's")
	}
	done(fs.ErrNotExist)
}

// The verbs this file drives are the ones the coverage table drives, so a verb
// that stops being measured and a verb that stops being bounded fail in one
// place rather than drifting apart.
func TestTheRecordedVerbsAreTheOutboundOnes(t *testing.T) {
	t.Parallel()

	sending, _ := portVerbs()
	if driven := slices.Sorted(maps.Keys(outbound())); !slices.Equal(driven, sending) {
		t.Errorf("verbs driven = %v, want the port's %v", driven, sending)
	}
}
