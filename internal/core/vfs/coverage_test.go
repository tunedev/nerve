package vfs_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// No linter finds an unbounded call, so this file is the check: it walks the
// port for every verb that can reach a remote, sends each one through the tree
// a caller actually holds, and fails any that outlives the bound. The verb list
// comes from the port by reflection rather than from a list kept by hand, so a
// verb added without a bound fails here instead of shipping.

// deaf is a plugin that answers nothing: every verb waits for its context to
// end and reports why. It is what a remote mount looks like when the far end
// accepts the call and then says nothing, and it is what turns "unbounded" from
// a reading of the code into a test result.
type deaf struct{}

var (
	_ vfs.FileSystem = deaf{}
	_ vfs.Symlinker  = deaf{}
	_ vfs.Streamer   = deaf{}
	_ vfs.Truncater  = deaf{}
	_ vfs.Subber     = deaf{}
)

func (deaf) wait(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

func (d deaf) Create(ctx context.Context, _ string) error    { return d.wait(ctx) }
func (d deaf) Mkdir(ctx context.Context, _ string) error     { return d.wait(ctx) }
func (d deaf) Remove(ctx context.Context, _ string) error    { return d.wait(ctx) }
func (d deaf) RemoveAll(ctx context.Context, _ string) error { return d.wait(ctx) }
func (d deaf) Write(ctx context.Context, _ string, _ []byte) error {
	return d.wait(ctx)
}
func (d deaf) Rename(ctx context.Context, _, _ string) error { return d.wait(ctx) }
func (d deaf) Chmod(ctx context.Context, _ string, _ fs.FileMode) error {
	return d.wait(ctx)
}
func (d deaf) Truncate(ctx context.Context, _ string, _ int64) error {
	return d.wait(ctx)
}
func (d deaf) Symlink(ctx context.Context, _, _ string) error { return d.wait(ctx) }
func (d deaf) Read(ctx context.Context, _ string) ([]byte, error) {
	return nil, d.wait(ctx)
}
func (d deaf) ReadDir(ctx context.Context, _ string) ([]vfs.FileInfo, error) {
	return nil, d.wait(ctx)
}
func (d deaf) Stat(ctx context.Context, _ string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, d.wait(ctx)
}
func (d deaf) Readlink(ctx context.Context, _ string) (string, error) {
	return "", d.wait(ctx)
}
func (d deaf) Lstat(ctx context.Context, _ string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, d.wait(ctx)
}
func (d deaf) OpenRead(ctx context.Context, _ string) (io.ReadCloser, error) {
	return nil, d.wait(ctx)
}
func (d deaf) OpenWrite(ctx context.Context, _ string) (io.WriteCloser, error) {
	return nil, d.wait(ctx)
}

func (deaf) Capabilities(string) vfs.Caps { return vfs.Caps{} }
func (d deaf) AsSymlinker() vfs.Symlinker { return d }
func (d deaf) AsStreamer() vfs.Streamer   { return d }
func (d deaf) AsTruncater() vfs.Truncater { return d }
func (d deaf) AsSubber() vfs.Subber       { return d }

// Sub answers rather than waiting, because it is not a call: it narrows the
// address space and reaches nothing.
func (d deaf) Sub(string) vfs.FileSystem { return d }

// outbound is every verb a caller can send out through a mount, spelled as the
// call a caller makes rather than as the method the plugin implements — the
// optional sets are reached through their accessors, which is the only way a
// caller has. TestEveryVerbThatSendsIsCovered keeps this list equal to the
// port's.
func outbound() map[string]func(context.Context, vfs.FileSystem) error {
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
		"Lstat": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := vfs.Lstat(ctx, f, "/x")
			return err
		},
		"Truncate": func(ctx context.Context, f vfs.FileSystem) error {
			return f.AsTruncater().Truncate(ctx, "/x", 0)
		},
		"OpenRead": func(ctx context.Context, f vfs.FileSystem) error {
			stream, err := f.AsStreamer().OpenRead(ctx, "/x")
			if err == nil {
				_ = stream.Close()
			}
			return err
		},
		"OpenWrite": func(ctx context.Context, f vfs.FileSystem) error {
			stream, err := f.AsStreamer().OpenWrite(ctx, "/x")
			if err == nil {
				_ = stream.Close()
			}
			return err
		},
	}
}

// quiet is the port's methods that send nothing. Capabilities answers from the
// shape a mount already knows, the accessors hand back a verb set, and Sub
// narrows the address space; none of them can reach a remote, which is why none
// takes a context and why none needs a bound.
var quiet = []string{"AsStreamer", "AsSubber", "AsSymlinker", "AsTruncater", "Capabilities", "Sub"}

// portVerbs splits the port and its four optional verb sets by whether a
// method takes a context, which is the port's own statement of what can reach
// something remote.
func portVerbs() (sending, silent []string) {
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()

	for _, iface := range []reflect.Type{
		reflect.TypeOf((*vfs.FileSystem)(nil)).Elem(),
		reflect.TypeOf((*vfs.Symlinker)(nil)).Elem(),
		reflect.TypeOf((*vfs.Streamer)(nil)).Elem(),
		reflect.TypeOf((*vfs.Truncater)(nil)).Elem(),
		reflect.TypeOf((*vfs.Subber)(nil)).Elem(),
	} {
		for i := range iface.NumMethod() {
			method := iface.Method(i)
			if method.Type.NumIn() > 0 && method.Type.In(0) == ctxType {
				sending = append(sending, method.Name)
				continue
			}
			silent = append(silent, method.Name)
		}
	}

	slices.Sort(sending)
	slices.Sort(silent)
	return slices.Compact(sending), slices.Compact(silent)
}

// The coverage half of the ticket: the verb list below is the port's, not one
// kept by hand. A verb added to the port arrives here unlisted and fails, which
// is the only warning anyone gets — no linter reads a call for its bound.
func TestEveryVerbThatSendsIsCovered(t *testing.T) {
	sending, silent := portVerbs()

	if covered := slices.Sorted(maps.Keys(outbound())); !slices.Equal(covered, sending) {
		t.Errorf("verbs sent by the coverage test = %v, want the port's %v", covered, sending)
	}
	if !slices.Equal(quiet, silent) {
		t.Errorf("port methods taking no context = %v, want %v; a new one has to be shown to send nothing before it is exempt", silent, quiet)
	}
}

// The other half: every one of those verbs, sent through the tree a caller
// actually holds, gives up at the bound. Driving the table the registry built
// rather than the wrapper alone is the point — the bound has to survive
// dispatch, not merely exist.
func TestNoOutboundCallOutlivesItsBound(t *testing.T) {
	const bound = 20 * time.Millisecond

	for name, send := range outbound() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err, answered := within(50*bound, func() error {
				return send(context.Background(), deafTree(t, bound))
			})

			if !answered {
				t.Fatalf("%s of a plugin that never answers is still waiting; it reaches the plugin unbounded", name)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s of a plugin that never answers = %v, want %v", name, err, context.DeadlineExceeded)
			}
		})
	}
}

// deafTree is the route a call really takes: a mount table the registry built,
// with a plugin behind it that never answers.
//
// Retry is off, so what a verb takes to fail here is one bound rather than a
// bound per attempt. This file asks whether a verb is bounded at all; what the
// attempts cost on top of that is retry's question, and retry_test.go asks it.
//
// The breaker is on, and set to open on the second failure in a row so that it
// never opens: each subtest builds its own tree and sends one verb through it.
// A live breaker is the point — every verb here passes through it, so one that
// the breaker forwarded to nothing would fail as an unbounded call rather than
// pass against a surface with the breaker switched off.
func deafTree(t *testing.T, d time.Duration) vfs.FileSystem {
	t.Helper()

	registry := vfs.NewRegistry(d, stability.Retry{}, stability.Breaker{Threshold: 2, Cooldown: time.Minute})
	if err := registry.Register("deaf", func(map[string]string) (vfs.FileSystem, error) {
		return deaf{}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "deaf"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	return table
}

// within gives send a limit of its own, so an unbounded verb fails as one call
// that never came back rather than as a suite that hangs until the test binary
// is killed. answered is false when the limit ran out first.
func within(limit time.Duration, send func() error) (err error, answered bool) {
	done := make(chan error, 1)
	go func() { done <- send() }()

	select {
	case err := <-done:
		return err, true
	case <-time.After(limit):
		return nil, false
	}
}
