package vfs_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// noting answers every verb by noting the deadline it was called with. A
// zero deadline is a call that arrived unbounded, which is the failure this
// ticket exists to prevent.
type noting struct {
	seen []time.Time
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
	return io.NopCloser(nil), nil
}
func (r *noting) OpenWrite(ctx context.Context, _ string) (io.WriteCloser, error) {
	r.at(ctx)
	return nil, nil
}

// verbs is every call that has to come back before the caller can continue.
// The streaming opens are absent on purpose: a stream outlives its open, so it
// is not one of these.
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

func TestACallThatNeverAnswersFailsAtTheDeadline(t *testing.T) {
	fsys := vfs.WithTimeout(blocking{}, time.Millisecond)

	_, err := fsys.Read(context.Background(), "/x")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Read of a plugin that never answers = %v, want %v", err, context.DeadlineExceeded)
	}
}

// A stream is not a call. Its context outlives the open, so bounding the open
// would cut a healthy transfer at the bound; the streaming verbs are forwarded
// with the caller's context as it arrived.
func TestAStreamIsNotBoundedByTheCallTimeout(t *testing.T) {
	note := &noting{}
	fsys := vfs.WithTimeout(note, time.Minute)

	stream := fsys.AsStreamer()
	if stream == nil {
		t.Fatal("AsStreamer() = nil, want the plugin's streaming verbs")
	}
	if _, err := stream.OpenRead(context.Background(), "/big"); err != nil {
		t.Fatalf("OpenRead: %v", err)
	}

	if at := note.seen[0]; !at.IsZero() {
		t.Errorf("OpenRead reached the plugin with deadline %s, want the caller's context unchanged", at)
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
	registry := vfs.NewRegistry(time.Minute)
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
