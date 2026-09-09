package vfs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// brisk retries three times with a wait short enough that a test does not
// notice it. The jitter is checked where it is computed, not by timing.
var brisk = stability.Retry{Attempts: 3, Backoff: time.Millisecond}

// flaky fails its first fail calls with err and answers after that, counting
// everything that reaches it. What a retry does is only visible as the number
// of attempts a plugin saw.
type flaky struct {
	fail  int
	err   error
	caps  vfs.Caps
	calls int
	// onCall runs before each answer, which is how a test cancels its caller
	// from inside the plugin.
	onCall func()
}

var _ vfs.FileSystem = &flaky{}

func (f *flaky) answer() error {
	f.calls++
	if f.onCall != nil {
		f.onCall()
	}
	if f.calls <= f.fail {
		return f.err
	}
	return nil
}

func (f *flaky) Create(context.Context, string) error        { return f.answer() }
func (f *flaky) Mkdir(context.Context, string) error         { return f.answer() }
func (f *flaky) Remove(context.Context, string) error        { return f.answer() }
func (f *flaky) RemoveAll(context.Context, string) error     { return f.answer() }
func (f *flaky) Write(context.Context, string, []byte) error { return f.answer() }
func (f *flaky) Rename(context.Context, string, string) error {
	return f.answer()
}
func (f *flaky) Chmod(context.Context, string, fs.FileMode) error { return f.answer() }
func (f *flaky) Read(context.Context, string) ([]byte, error) {
	return nil, f.answer()
}
func (f *flaky) ReadDir(context.Context, string) ([]vfs.FileInfo, error) {
	return nil, f.answer()
}
func (f *flaky) Stat(context.Context, string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, f.answer()
}

func (f *flaky) Capabilities(string) vfs.Caps { return f.caps }

func (f *flaky) AsSymlinker() vfs.Symlinker { return f }
func (f *flaky) AsStreamer() vfs.Streamer   { return f }
func (f *flaky) AsTruncater() vfs.Truncater { return f }
func (f *flaky) AsSubber() vfs.Subber       { return nil }

func (f *flaky) Symlink(context.Context, string, string) error { return f.answer() }
func (f *flaky) Readlink(context.Context, string) (string, error) {
	return "/elsewhere", f.answer()
}
func (f *flaky) Lstat(context.Context, string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, f.answer()
}
func (f *flaky) Truncate(context.Context, string, int64) error { return f.answer() }
func (f *flaky) OpenRead(context.Context, string) (io.ReadCloser, error) {
	if err := f.answer(); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("content")), nil
}
func (f *flaky) OpenWrite(context.Context, string) (io.WriteCloser, error) {
	if err := f.answer(); err != nil {
		return nil, err
	}
	return discard{}, nil
}

// stalled is a plugin that never answers, counting the attempts that reach it.
// It is what a remote mount looks like when the far end has stopped talking.
type stalled struct {
	stub
	vfs.NoAccessors
	calls int
}

func (s *stalled) Read(ctx context.Context, _ string) ([]byte, error) {
	s.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

// errTransient is what a plugin returns when the far end wobbled.
var errTransient = fmt.Errorf("dial the far end: %w", stability.ErrTransient)

// The acceptance criterion, over every verb: a validation error reaches the
// plugin once. Repeating an invalid call cannot make it valid, so a retry
// there is a slower failure and load on a dependency that gained nothing.
func TestAValidationErrorIsNeverRetried(t *testing.T) {
	for name, call := range verbs() {
		t.Run(name, func(t *testing.T) {
			plugin := &flaky{fail: 99, err: fs.ErrInvalid}

			err := call(context.Background(), vfs.WithRetry(plugin, brisk))

			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s = %v, want %v", name, err, fs.ErrInvalid)
			}
			if plugin.calls != 1 {
				t.Errorf("%s reached the plugin %d times, want 1", name, plugin.calls)
			}
		})
	}
}

// The other half of the classification: every io/fs sentinel is an answer
// about the world, and none of them is retried.
func TestAnAnswerAboutTheWorldIsNeverRetried(t *testing.T) {
	for _, sentinel := range []error{fs.ErrNotExist, fs.ErrExist, fs.ErrPermission} {
		plugin := &flaky{fail: 99, err: sentinel}

		if _, err := vfs.WithRetry(plugin, brisk).Read(context.Background(), "/x"); !errors.Is(err, sentinel) {
			t.Errorf("Read = %v, want %v", err, sentinel)
		}
		if plugin.calls != 1 {
			t.Errorf("Read with %v reached the plugin %d times, want 1", sentinel, plugin.calls)
		}
	}
}

// A transient failure is what retrying is for.
func TestATransientFailureIsAttemptedAgain(t *testing.T) {
	plugin := &flaky{fail: 2, err: errTransient}

	if _, err := vfs.WithRetry(plugin, brisk).Read(context.Background(), "/x"); err != nil {
		t.Fatalf("Read = %v, want it to answer on the third attempt", err)
	}
	if plugin.calls != 3 {
		t.Errorf("Read reached the plugin %d times, want 3", plugin.calls)
	}
}

// Retrying is bounded by the attempts it was given, and what the caller sees
// is the last attempt's error rather than a summary of all of them.
func TestRetryingStopsAtTheAttemptsItWasGiven(t *testing.T) {
	plugin := &flaky{fail: 99, err: errTransient}

	err := vfs.WithRetry(plugin, brisk).Create(context.Background(), "/x")

	if !errors.Is(err, stability.ErrTransient) {
		t.Errorf("Create = %v, want %v", err, stability.ErrTransient)
	}
	if plugin.calls != brisk.Attempts {
		t.Errorf("Create reached the plugin %d times, want %d", plugin.calls, brisk.Attempts)
	}
}

// Repeating these is not repeating the same call. A Remove that succeeded and
// then failed to say so reports a missing entry on the second attempt, a
// Rename reports a missing source, and a Symlink reports a link already there
// — three false failures, and in the middle case a second entry moved.
func TestAVerbThatCannotBeRepeatedIsAttemptedOnce(t *testing.T) {
	once := map[string]func(context.Context, vfs.FileSystem) error{
		"Remove":  func(ctx context.Context, f vfs.FileSystem) error { return f.Remove(ctx, "/x") },
		"Rename":  func(ctx context.Context, f vfs.FileSystem) error { return f.Rename(ctx, "/x", "/y") },
		"Symlink": func(ctx context.Context, f vfs.FileSystem) error { return f.AsSymlinker().Symlink(ctx, "/x", "/y") },
	}

	for name, call := range once {
		t.Run(name, func(t *testing.T) {
			plugin := &flaky{fail: 1, err: errTransient}

			if err := call(context.Background(), vfs.WithRetry(plugin, brisk)); !errors.Is(err, stability.ErrTransient) {
				t.Errorf("%s = %v, want the plugin's error", name, err)
			}
			if plugin.calls != 1 {
				t.Errorf("%s reached the plugin %d times, want 1", name, plugin.calls)
			}
		})
	}
}

// Whether a verb can be repeated is a property of the path, not of the method
// name: a queue's job path is consumed by reading, so a Read that took an
// entry and then failed to deliver it loses that entry on the second attempt.
func TestADestructiveReadIsAttemptedOnce(t *testing.T) {
	plugin := &flaky{fail: 1, err: errTransient, caps: vfs.Caps{ReadDestructive: true}}
	fsys := vfs.WithRetry(plugin, brisk)

	if _, err := fsys.Read(context.Background(), "/jobs/next"); !errors.Is(err, stability.ErrTransient) {
		t.Errorf("Read = %v, want the plugin's error", err)
	}
	if plugin.calls != 1 {
		t.Errorf("a destructive Read reached the plugin %d times, want 1", plugin.calls)
	}

	stream := &flaky{fail: 1, err: errTransient, caps: vfs.Caps{ReadDestructive: true}}
	if _, err := vfs.WithRetry(stream, brisk).AsStreamer().OpenRead(context.Background(), "/jobs/next"); err == nil {
		t.Error("OpenRead answered, want the plugin's error")
	}
	if stream.calls != 1 {
		t.Errorf("a destructive OpenRead reached the plugin %d times, want 1", stream.calls)
	}
}

// An append that failed may have appended. Repeating it writes the same bytes
// twice, which is a different file from the one the caller asked for.
func TestAnAppendIsAttemptedOnce(t *testing.T) {
	plugin := &flaky{fail: 1, err: errTransient, caps: vfs.Caps{AppendOnly: true}}
	fsys := vfs.WithRetry(plugin, brisk)

	if err := fsys.Write(context.Background(), "/log/today", []byte("line")); !errors.Is(err, stability.ErrTransient) {
		t.Errorf("Write = %v, want the plugin's error", err)
	}
	if plugin.calls != 1 {
		t.Errorf("an appending Write reached the plugin %d times, want 1", plugin.calls)
	}

	stream := &flaky{fail: 1, err: errTransient, caps: vfs.Caps{AppendOnly: true}}
	if _, err := vfs.WithRetry(stream, brisk).AsStreamer().OpenWrite(context.Background(), "/log/today"); err == nil {
		t.Error("OpenWrite answered, want the plugin's error")
	}
	if stream.calls != 1 {
		t.Errorf("an appending OpenWrite reached the plugin %d times, want 1", stream.calls)
	}
}

// A path that replaces rather than appends is written again, which is the same
// verb on the same wrapper taking the other branch.
func TestAWholeContentWriteIsAttemptedAgain(t *testing.T) {
	plugin := &flaky{fail: 2, err: errTransient}

	if err := vfs.WithRetry(plugin, brisk).Write(context.Background(), "/x", []byte("content")); err != nil {
		t.Fatalf("Write = %v, want it to answer on the third attempt", err)
	}
	if plugin.calls != 3 {
		t.Errorf("Write reached the plugin %d times, want 3", plugin.calls)
	}
}

// An open that failed transferred nothing, so it is a call like any other and
// the stream it eventually returns is the plugin's.
func TestAnOpenIsAttemptedAgain(t *testing.T) {
	plugin := &flaky{fail: 2, err: errTransient}

	stream, err := vfs.WithRetry(plugin, brisk).AsStreamer().OpenRead(context.Background(), "/big")
	if err != nil {
		t.Fatalf("OpenRead = %v, want it to answer on the third attempt", err)
	}
	defer stream.Close()

	if plugin.calls != 3 {
		t.Errorf("OpenRead reached the plugin %d times, want 3", plugin.calls)
	}
	content, err := io.ReadAll(stream)
	if err != nil || string(content) != "content" {
		t.Errorf("the stream read %q, %v; want the plugin's content", content, err)
	}
}

// A caller that has gone gets no further attempts, and the wait between them
// ends with it rather than running to the end of the backoff.
func TestACallerThatGaveUpStopsTheRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	plugin := &flaky{fail: 99, err: errTransient, onCall: cancel}
	patient := stability.Retry{Attempts: 5, Backoff: time.Minute}

	start := time.Now()
	_, err := vfs.WithRetry(plugin, patient).Stat(ctx, "/x")
	elapsed := time.Since(start)

	if !errors.Is(err, stability.ErrTransient) {
		t.Errorf("Stat = %v, want the plugin's error", err)
	}
	if plugin.calls != 1 {
		t.Errorf("a cancelled Stat reached the plugin %d times, want 1", plugin.calls)
	}
	if elapsed > 10*time.Second {
		t.Errorf("a cancelled Stat waited %s, want it to give up with its caller", elapsed)
	}
}

// Retrying a plugin says nothing about what it can do: a refused verb set is
// still refused, so the caller's nil guard keeps telling the truth.
func TestRetryingAPluginRefusesWhatItRefused(t *testing.T) {
	fsys := vfs.WithRetry(plain{}, brisk)

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

// Capabilities is not I/O and there is nothing to attempt again. It reaches
// the plugin unchanged, which is also how the wrapper knows what it may repeat.
func TestRetryingReadsCapabilitiesFromThePlugin(t *testing.T) {
	fsys := vfs.WithRetry(&flaky{caps: vfs.Caps{ReadOnly: true}}, brisk)

	if got := fsys.Capabilities("/x"); got != (vfs.Caps{ReadOnly: true}) {
		t.Errorf("Capabilities() = %+v, want the plugin's", got)
	}
}

// Retrying is structural rather than a habit, the way bounding is: a plugin
// reaches the tree only through the registry, and what the registry hands back
// has already been wrapped.
func TestTheRegistryRetriesWhatItBuilds(t *testing.T) {
	plugin := &flaky{fail: 2, err: errTransient}
	registry := vfs.NewRegistry(time.Minute, brisk, stability.Breaker{})
	if err := registry.Register("flaky", func(map[string]string) (vfs.FileSystem, error) {
		return plugin, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "flaky"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if err := table.Create(context.Background(), "/x"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if plugin.calls != 3 {
		t.Errorf("a plugin the registry built saw %d attempts, want 3", plugin.calls)
	}
}

// Each attempt is a call and gets its own deadline: the bound sits inside the
// retry, so one attempt that hangs costs one deadline rather than the whole
// sequence, and the attempt after it starts with a full one.
func TestEachAttemptIsBoundedOnItsOwn(t *testing.T) {
	const bound = 20 * time.Millisecond

	plugin := &stalled{}
	registry := vfs.NewRegistry(bound, stability.Retry{Attempts: 3, Backoff: time.Millisecond}, stability.Breaker{})
	if err := registry.Register("stalled", func(map[string]string) (vfs.FileSystem, error) {
		return plugin, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	fsys, err := registry.Build(vfs.MountSpec{Prefix: "/", Kind: "stalled"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	start := time.Now()
	if _, err := fsys.Read(context.Background(), "/x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Read = %v, want %v", err, context.DeadlineExceeded)
	}

	if elapsed := time.Since(start); elapsed < 2*bound {
		t.Errorf("three attempts against a plugin that never answers took %s, want at least %s: the bound is being shared rather than given to each attempt", elapsed, 2*bound)
	}
	if plugin.calls != 3 {
		t.Errorf("a plugin that never answers saw %d attempts, want 3", plugin.calls)
	}
}
