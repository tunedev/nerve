package vfs_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// noting answers every verb by noting the deadline it was called with. A
// zero deadline is a call that arrived unbounded, which is the failure this
// ticket exists to prevent.
type noting struct {
	seen []time.Time
	// transferErr reports on the context the last stream was opened on, which
	// is the context that transfer runs on after its open has returned.
	transferErr func() error
}

var _ vfs.FileSystem = &noting{}

func (r *noting) at(ctx context.Context) {
	at, _ := ctx.Deadline()
	r.seen = append(r.seen, at)
}

func (r *noting) Create(ctx context.Context, _ string) error    { r.at(ctx); return nil }
func (r *noting) Mkdir(ctx context.Context, _ string) error     { r.at(ctx); return nil }
func (r *noting) Remove(ctx context.Context, _ string) error    { r.at(ctx); return nil }
func (r *noting) RemoveAll(ctx context.Context, _ string) error { r.at(ctx); return nil }
func (r *noting) Write(ctx context.Context, _ string, _ []byte) error {
	r.at(ctx)
	return nil
}
func (r *noting) Rename(ctx context.Context, _, _ string) error { r.at(ctx); return nil }
func (r *noting) Chmod(ctx context.Context, _ string, _ fs.FileMode) error {
	r.at(ctx)
	return nil
}
func (r *noting) Read(ctx context.Context, _ string) ([]byte, error) {
	r.at(ctx)
	return nil, nil
}
func (r *noting) ReadDir(ctx context.Context, _ string) ([]vfs.FileInfo, error) {
	r.at(ctx)
	return nil, nil
}
func (r *noting) Stat(ctx context.Context, _ string) (vfs.FileInfo, error) {
	r.at(ctx)
	return vfs.FileInfo{}, nil
}

func (r *noting) Capabilities(string) vfs.Caps { return vfs.Caps{ReadOnly: true} }

func (r *noting) AsSymlinker() vfs.Symlinker { return r }
func (r *noting) AsStreamer() vfs.Streamer   { return r }
func (r *noting) AsTruncater() vfs.Truncater { return r }

func (r *noting) Symlink(ctx context.Context, _, _ string) error { r.at(ctx); return nil }
func (r *noting) Readlink(ctx context.Context, _ string) (string, error) {
	r.at(ctx)
	return "/elsewhere", nil
}
func (r *noting) Truncate(ctx context.Context, _ string, _ int64) error {
	r.at(ctx)
	return nil
}
func (r *noting) OpenRead(ctx context.Context, _ string) (io.ReadCloser, error) {
	r.at(ctx)
	r.transferErr = ctx.Err
	return io.NopCloser(strings.NewReader("content")), nil
}
func (r *noting) OpenWrite(ctx context.Context, _ string) (io.WriteCloser, error) {
	r.at(ctx)
	r.transferErr = ctx.Err
	return discard{}, nil
}

// discard is a stream that accepts whatever is written to it, so a test can
// close one without a plugin behind it.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
func (discard) Close() error                { return nil }

// verbs is every call whose deadline reaches the plugin in its context. The
// streaming opens are absent on purpose: they are bounded too, but the bound is
// lifted when they answer, so it is not a deadline the plugin can read.
func verbs() map[string]func(context.Context, vfs.FileSystem) error {
	return map[string]func(context.Context, vfs.FileSystem) error{
		"Create":    func(ctx context.Context, f vfs.FileSystem) error { return f.Create(ctx, "/x") },
		"Mkdir":     func(ctx context.Context, f vfs.FileSystem) error { return f.Mkdir(ctx, "/x") },
		"Remove":    func(ctx context.Context, f vfs.FileSystem) error { return f.Remove(ctx, "/x") },
		"RemoveAll": func(ctx context.Context, f vfs.FileSystem) error { return f.RemoveAll(ctx, "/x") },
		"Write":     func(ctx context.Context, f vfs.FileSystem) error { return f.Write(ctx, "/x", nil) },
		"Rename":    func(ctx context.Context, f vfs.FileSystem) error { return f.Rename(ctx, "/x", "/y") },
		"Chmod":     func(ctx context.Context, f vfs.FileSystem) error { return f.Chmod(ctx, "/x", 0o644) },
		"Read": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := f.Read(ctx, "/x")
			return err
		},
		"ReadDir": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := f.ReadDir(ctx, "/x")
			return err
		},
		"Stat": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := f.Stat(ctx, "/x")
			return err
		},
		"Symlink": func(ctx context.Context, f vfs.FileSystem) error {
			return f.AsSymlinker().Symlink(ctx, "/x", "/y")
		},
		"Readlink": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := f.AsSymlinker().Readlink(ctx, "/x")
			return err
		},
		"Truncate": func(ctx context.Context, f vfs.FileSystem) error {
			return f.AsTruncater().Truncate(ctx, "/x", 0)
		},
	}
}

// The whole ticket in one test: no verb reaches a plugin without a deadline,
// and the deadline is the one the mount was given.
func TestNoVerbReachesThePluginUnbounded(t *testing.T) {
	const bound = time.Minute

	for name, call := range verbs() {
		t.Run(name, func(t *testing.T) {
			note := &noting{}
			fsys := vfs.WithTimeout(note, bound)

			before := time.Now()
			if err := call(context.Background(), fsys); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			after := time.Now()

			if len(note.seen) != 1 {
				t.Fatalf("%s reached the plugin %d times, want 1", name, len(note.seen))
			}
			switch at := note.seen[0]; {
			case at.IsZero():
				t.Errorf("%s reached the plugin with no deadline", name)
			case at.Before(before.Add(bound)), at.After(after.Add(bound)):
				t.Errorf("%s deadline is %s from the call, want %s", name, at.Sub(before), bound)
			}
		})
	}
}

// A deadline propagates: the mount's bound is a ceiling on the caller's, never
// an extension of it, so a handler with less time left keeps it.
func TestTheCallersEarlierDeadlineWins(t *testing.T) {
	note := &noting{}
	fsys := vfs.WithTimeout(note, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	caller, _ := ctx.Deadline()

	if err := fsys.Create(ctx, "/x"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if at := note.seen[0]; !at.Equal(caller) {
		t.Errorf("plugin saw deadline %s, want the caller's %s", at, caller)
	}
}

// blocking is a plugin that never answers, which is what a remote mount looks
// like when the far end has stopped talking.
type blocking struct {
	stub
	vfs.NoAccessors
}

func (blocking) Read(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// hanging is a plugin whose opens never return, which is what a remote mount
// looks like when the far end accepts a connection and then says nothing.
type hanging struct {
	stub
	vfs.NoAccessors
}

func (h hanging) AsStreamer() vfs.Streamer { return h }

func (hanging) OpenRead(ctx context.Context, _ string) (io.ReadCloser, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (hanging) OpenWrite(ctx context.Context, _ string) (io.WriteCloser, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestACallThatNeverAnswersFailsAtTheDeadline(t *testing.T) {
	fsys := vfs.WithTimeout(blocking{}, time.Millisecond)

	_, err := fsys.Read(context.Background(), "/x")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Read of a plugin that never answers = %v, want %v", err, context.DeadlineExceeded)
	}
}

// An open is a call and gets a deadline; the stream it returns is not, and
// keeps running on the caller's context. Bounding the transfer with the open's
// deadline would cut a healthy download at the bound.
func TestAStreamOutlivesTheBoundOnItsOpen(t *testing.T) {
	note := &noting{}
	fsys := vfs.WithTimeout(note, 10*time.Millisecond)

	stream, err := fsys.AsStreamer().OpenRead(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer stream.Close()

	time.Sleep(50 * time.Millisecond)

	if err := note.transferErr(); err != nil {
		t.Errorf("the transfer ended %s after its open, want it still running: %v", 50*time.Millisecond, err)
	}
	if _, err := io.ReadAll(stream); err != nil {
		t.Errorf("reading past the bound: %v", err)
	}
}

// An open that never answers is an unbounded call, which the bound exists to
// prevent. The stream lifetime being separate is not a reason to let one hang.
func TestAnOpenThatNeverAnswersFailsAtTheBound(t *testing.T) {
	fsys := vfs.WithTimeout(hanging{}, time.Millisecond)

	if _, err := fsys.AsStreamer().OpenRead(context.Background(), "/big"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("OpenRead of a plugin that never opens = %v, want %v", err, context.DeadlineExceeded)
	}
	if _, err := fsys.AsStreamer().OpenWrite(context.Background(), "/big"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("OpenWrite of a plugin that never opens = %v, want %v", err, context.DeadlineExceeded)
	}
}

// Closing the stream ends the transfer, so a finished download stops holding
// the context its caller gave it.
func TestClosingAStreamEndsItsTransfer(t *testing.T) {
	note := &noting{}
	fsys := vfs.WithTimeout(note, time.Minute)

	stream, err := fsys.AsStreamer().OpenWrite(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := note.transferErr(); !errors.Is(err, context.Canceled) {
		t.Errorf("the transfer after Close = %v, want %v", err, context.Canceled)
	}
}

// The transfer is untied from the open's deadline, not from its caller: a
// cancelled request still stops a download in flight.
func TestCancellingTheCallerStopsTheTransfer(t *testing.T) {
	note := &noting{}
	fsys := vfs.WithTimeout(note, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := fsys.AsStreamer().OpenRead(ctx, "/big")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer stream.Close()

	cancel()

	if err := note.transferErr(); !errors.Is(err, context.Canceled) {
		t.Errorf("the transfer after the caller cancelled = %v, want %v", err, context.Canceled)
	}
}

// Bounding a plugin says nothing about what it can do: a refused verb set is
// still refused, so the caller's nil guard keeps telling the truth.
func TestBoundingAPluginRefusesWhatItRefused(t *testing.T) {
	fsys := vfs.WithTimeout(plain{}, time.Minute)

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

// Capabilities is not I/O and has no context to bound. It reaches the plugin
// unchanged.
func TestCapabilitiesComeFromThePlugin(t *testing.T) {
	fsys := vfs.WithTimeout(&noting{}, time.Minute)

	if got := fsys.Capabilities("/x"); got != (vfs.Caps{ReadOnly: true}) {
		t.Errorf("Capabilities() = %+v, want the plugin's", got)
	}
}

// Bounding is structural rather than a habit: a plugin reaches the tree only
// through the registry, and the registry hands back a bounded one.
func TestTheRegistryBuildsNothingUnbounded(t *testing.T) {
	note := &noting{}
	registry := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{})
	if err := registry.Register("noting", func(map[string]string) (vfs.FileSystem, error) {
		return note, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "noting"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if err := table.Create(context.Background(), "/x"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if note.seen[0].IsZero() {
		t.Error("a plugin the registry built reached the plugin unbounded")
	}
}
