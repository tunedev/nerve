package vfs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// serving is a plugin whose streaming read hands back content a piece at a
// time. Every pull is reported so a test can see how far in front of the caller
// the fetch is running, and gap is what a source that costs latency per piece
// looks like.
type serving struct {
	stub
	vfs.NoAccessors
	content []byte
	gap     time.Duration
	pulls   chan struct{}
	caps    vfs.Caps
	fail    error
	refuse  error
	closes  atomic.Int32
}

func (s *serving) Capabilities(string) vfs.Caps { return s.caps }
func (s *serving) AsStreamer() vfs.Streamer     { return s }
func (s *serving) AsSubber() vfs.Subber         { return nil }

func (s *serving) OpenRead(context.Context, string) (io.ReadCloser, error) {
	return &source{from: s}, nil
}

func (s *serving) OpenWrite(context.Context, string) (io.WriteCloser, error) {
	return nil, fs.ErrPermission
}

// source is one transfer off serving. Only the fetch reads it, so the offset
// needs no guard; the report is a send that never blocks, because a test that
// stopped listening must not strand the fetch behind it.
type source struct {
	from   *serving
	offset int
}

func (s *source) Read(into []byte) (int, error) {
	select {
	case s.from.pulls <- struct{}{}:
	default:
	}
	time.Sleep(s.from.gap)

	if s.offset >= len(s.from.content) {
		if s.from.fail != nil {
			return 0, s.from.fail
		}
		return 0, io.EOF
	}
	n := copy(into, s.from.content[s.offset:])
	s.offset += n
	return n, nil
}

func (s *source) Close() error {
	s.from.closes.Add(1)
	return s.from.refuse
}

// pulled reports whether one more pull arrived within a moment. It is how a
// test says "the fetch did run in front" and "the fetch did not", and the wait
// is long enough that a goroutine which was going to start already has.
func pulled(pulls chan struct{}) bool {
	select {
	case <-pulls:
		return true
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

// opened is the stream a plugin hands back through the readahead wrapper, ready
// to read. The window is bytes, and zero turns reading ahead off.
func opened(t *testing.T, plugin vfs.FileSystem, window int) io.ReadCloser {
	t.Helper()

	stream, err := vfs.WithReadahead(plugin, window).AsStreamer().OpenRead(context.Background(), "/x")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

// The whole point, and the thing a benchmark only quantifies: the source is
// being read while the caller has read nothing at all. Without the fetch in
// front, nothing reaches the source until the caller asks.
func TestTheFetchRunsInFrontOfTheCaller(t *testing.T) {
	const window = 4 * 32 * 1024

	plugin := &serving{content: make([]byte, window), pulls: make(chan struct{}, 64)}
	opened(t, plugin, window)

	for pull := 1; pull <= 4; pull++ {
		if !pulled(plugin.pulls) {
			t.Fatalf("the source was pulled %d times before the caller read anything, want 4", pull-1)
		}
	}
}

func TestWithNoWindowNothingIsReadUntilTheCallerAsks(t *testing.T) {
	plugin := &serving{content: make([]byte, 128*1024), pulls: make(chan struct{}, 64)}
	stream := opened(t, plugin, 0)

	if pulled(plugin.pulls) {
		t.Fatal("the source was read with the window off; nothing should reach it until the caller asks")
	}
	if _, err := stream.Read(make([]byte, 1)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !pulled(plugin.pulls) {
		t.Error("the caller read and the source was not; the plugin is not being reached at all")
	}
}

// A path a read consumes is never read ahead of. What the queue holds when the
// caller closes is dropped, and there dropping it would lose the job.
func TestAPathAReadConsumesIsNotReadAheadOf(t *testing.T) {
	plugin := &serving{
		content: make([]byte, 128*1024),
		pulls:   make(chan struct{}, 64),
		caps:    vfs.Caps{ReadDestructive: true},
	}
	opened(t, plugin, 128*1024)

	if pulled(plugin.pulls) {
		t.Error("a path a read consumes was read ahead of; the queue would drop what the caller never took")
	}
}

// Reading ahead is only ever about when bytes are fetched. What arrives has to
// be the source's content, byte for byte, however the caller asks for it.
func TestTheCallerReceivesTheSourceUnchanged(t *testing.T) {
	content := make([]byte, 300*1024)
	for i := range content {
		content[i] = byte(rand.IntN(256))
	}

	for _, window := range []int{1, 32 * 1024, 256 * 1024} {
		stream := opened(t, &serving{content: content, pulls: make(chan struct{}, 1)}, window)

		read, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("window %d: ReadAll: %v", window, err)
		}
		if !bytes.Equal(read, content) {
			t.Errorf("window %d: read %d bytes, want the source's %d", window, len(read), len(content))
		}
	}
}

// A source that fails partway hands over what it moved before it says so, and
// says so on every read after that. A caller that was told nothing about the
// bytes already fetched would have lost them to the wrapper.
func TestWhatMovedArrivesBeforeTheFailure(t *testing.T) {
	refused := errors.New("the far end hung up")
	content := []byte("what crossed before it broke")

	stream := opened(t, &serving{content: content, pulls: make(chan struct{}, 1), fail: refused}, 64*1024)

	read, err := io.ReadAll(stream)
	if !errors.Is(err, refused) {
		t.Errorf("ReadAll of a source that failed = %v, want %v", err, refused)
	}
	if !bytes.Equal(read, content) {
		t.Errorf("read %q before the failure, want %q", read, content)
	}
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, refused) {
		t.Errorf("the read after the failure = %v, want the failure again", err)
	}
}

// Closing is the whole of how the fetch is stopped: it closes the source, which
// is what ends a pull already in flight, and reports what the source said.
// TestMain fails the package if the goroutine outlived it.
func TestClosingEndsTheFetchAndReportsTheSource(t *testing.T) {
	refused := errors.New("the source would not close")
	plugin := &serving{content: make([]byte, 512*1024), pulls: make(chan struct{}, 64), refuse: refused}

	stream, err := vfs.WithReadahead(plugin, 64*1024).AsStreamer().OpenRead(context.Background(), "/x")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}

	if err := stream.Close(); !errors.Is(err, refused) {
		t.Errorf("Close = %v, want the source's %v", err, refused)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("closing twice = %v, want nil; the second close has nothing left to do", err)
	}
	if closes := plugin.closes.Load(); closes != 1 {
		t.Errorf("the source was closed %d times, want once", closes)
	}
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("reading a closed stream = %v, want %v", err, fs.ErrClosed)
	}
}

// Reading ahead says nothing about what a plugin can do. A plugin with no
// streaming verbs still has none, so the caller's nil guard keeps telling the
// truth.
func TestAPluginWithNoStreamingVerbsStillHasNone(t *testing.T) {
	if stream := vfs.WithReadahead(bare{}, 64*1024).AsStreamer(); stream != nil {
		t.Errorf("AsStreamer of a plugin with no streaming verbs = %v, want nil", stream)
	}
}

// bare is a plugin that answers no optional verb set at all.
type bare struct {
	stub
	vfs.NoAccessors
}

// tree is plugin mounted at the root of a table the registry built, which is
// what a caller actually holds. window is what the registry was told.
func tree(t *testing.T, plugin vfs.FileSystem, window int) vfs.FileSystem {
	t.Helper()

	registry := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{Threshold: 2, Cooldown: time.Minute})
	registry.ReadAhead(window)
	if err := registry.Register("serving", func(map[string]string) (vfs.FileSystem, error) {
		return plugin, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "serving"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	return table
}

// The fetch has to survive dispatch. Every caller holds the mount table rather
// than a plugin, and a wrapper the table does not forward through is a wrapper
// that does nothing.
func TestTheFetchSurvivesTheMountTable(t *testing.T) {
	for name, window := range map[string]int{"on": 128 * 1024, "off": 0} {
		t.Run(name, func(t *testing.T) {
			plugin := &serving{content: make([]byte, 128*1024), pulls: make(chan struct{}, 64)}

			stream, err := tree(t, plugin, window).AsStreamer().OpenRead(context.Background(), "/x")
			if err != nil {
				t.Fatalf("OpenRead: %v", err)
			}
			defer func() { _ = stream.Close() }()

			if ahead := pulled(plugin.pulls); ahead != (window > 0) {
				t.Errorf("the source was read before the caller = %v, want %v for a window of %d", ahead, window > 0, window)
			}
		})
	}
}

// The measurement E6.8 turns on. The source costs latency per piece and the
// caller costs work per piece; read one after the other that is the sum, and
// with the fetch in front it is the larger of the two.
func BenchmarkStreamOverALatentSource(b *testing.B) {
	const (
		pieces  = 16
		latency = 2 * time.Millisecond
		work    = 2 * time.Millisecond
	)

	for _, window := range []struct {
		name  string
		bytes int
	}{
		{"off", 0},
		{"one-fetch", 32 * 1024},
		{"default", vfs.DefaultReadahead},
	} {
		b.Run(window.name, func(b *testing.B) {
			plugin := &serving{
				content: make([]byte, pieces*32*1024),
				gap:     latency,
				pulls:   make(chan struct{}, 1),
			}
			fsys := vfs.WithReadahead(plugin, window.bytes)

			for b.Loop() {
				stream, err := fsys.AsStreamer().OpenRead(context.Background(), "/x")
				if err != nil {
					b.Fatalf("OpenRead: %v", err)
				}
				into := make([]byte, 32*1024)
				for {
					n, err := stream.Read(into)
					if n > 0 {
						time.Sleep(work)
					}
					if err != nil {
						break
					}
				}
				_ = stream.Close()
			}
		})
	}
}

// The other half of the measurement, and the one the ticket names: what the
// caller waits for on its very first read. A caller does something between
// opening a stream and reading it — writing response headers, building a hash —
// and with the fetch in front that work and the source's first piece happen at
// once instead of one after the other.
func BenchmarkFirstReadAfterOpen(b *testing.B) {
	const (
		latency = 2 * time.Millisecond
		setup   = 2 * time.Millisecond
	)

	for _, window := range []struct {
		name  string
		bytes int
	}{
		{"off", 0},
		{"default", vfs.DefaultReadahead},
	} {
		b.Run(window.name, func(b *testing.B) {
			plugin := &serving{
				content: make([]byte, 512*1024),
				gap:     latency,
				pulls:   make(chan struct{}, 1),
			}
			fsys := vfs.WithReadahead(plugin, window.bytes)
			into := make([]byte, 32*1024)

			for b.Loop() {
				b.StopTimer()
				stream, err := fsys.AsStreamer().OpenRead(context.Background(), "/x")
				if err != nil {
					b.Fatalf("OpenRead: %v", err)
				}
				time.Sleep(setup)
				b.StartTimer()

				if _, err := stream.Read(into); err != nil {
					b.Fatalf("Read: %v", err)
				}

				b.StopTimer()
				_ = stream.Close()
				b.StartTimer()
			}
		})
	}
}
