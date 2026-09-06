package vfs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// errBroken is a plugin that will not do the work, and errUnheard is an
// observer that cannot be told about it.
var (
	errBroken  = errors.New("the plugin refused the call")
	errUnheard = errors.New("the observer could not be told")
)

// writable answers every verb with the error it was given and says whatever it
// was told to say about a path. It has every optional verb set, so the
// accessors are observed too.
type writable struct {
	refuse error
	caps   vfs.Caps
}

var (
	_ vfs.FileSystem = writable{}
	_ vfs.Symlinker  = writable{}
	_ vfs.Streamer   = writable{}
	_ vfs.Truncater  = writable{}
)

func (w writable) Create(context.Context, string) error        { return w.refuse }
func (w writable) Mkdir(context.Context, string) error         { return w.refuse }
func (w writable) Remove(context.Context, string) error        { return w.refuse }
func (w writable) RemoveAll(context.Context, string) error     { return w.refuse }
func (w writable) Write(context.Context, string, []byte) error { return w.refuse }
func (w writable) Rename(context.Context, string, string) error {
	return w.refuse
}
func (w writable) Chmod(context.Context, string, fs.FileMode) error { return w.refuse }
func (w writable) Truncate(context.Context, string, int64) error    { return w.refuse }
func (w writable) Symlink(context.Context, string, string) error    { return w.refuse }

func (w writable) Read(context.Context, string) ([]byte, error) { return nil, w.refuse }
func (w writable) ReadDir(context.Context, string) ([]vfs.FileInfo, error) {
	return nil, w.refuse
}
func (w writable) Stat(context.Context, string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, w.refuse
}
func (w writable) Readlink(context.Context, string) (string, error) { return "", w.refuse }
func (w writable) Lstat(context.Context, string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, w.refuse
}

func (w writable) OpenRead(context.Context, string) (io.ReadCloser, error) {
	if w.refuse != nil {
		return nil, w.refuse
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (w writable) OpenWrite(context.Context, string) (io.WriteCloser, error) {
	if w.refuse != nil {
		return nil, w.refuse
	}
	return discard{}, nil
}

func (w writable) Capabilities(string) vfs.Caps { return w.caps }
func (w writable) AsSymlinker() vfs.Symlinker   { return w }
func (w writable) AsStreamer() vfs.Streamer     { return w }
func (w writable) AsSubber() vfs.Subber         { return nil }
func (w writable) AsTruncater() vfs.Truncater   { return w }

// told is what the observer was told, in the order it was told, and what it
// says back.
type told struct {
	mu     sync.Mutex
	paths  []string
	refuse error
}

var _ vfs.WriteObserver = (*told)(nil)

func (t *told) Wrote(_ context.Context, path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.paths = append(t.paths, path)
	return t.refuse
}

func (t *told) reported() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.paths)
}

// change is one call and the paths it changes.
type change struct {
	send  func(context.Context, vfs.FileSystem) error
	paths []string
}

// mutating is every call that changes what a path holds, with the paths it
// changes. A streamed write is opened and closed here, because the write it
// stands in for is not complete until Close returns.
func mutating() map[string]change {
	return map[string]change{
		"Create":    {func(ctx context.Context, f vfs.FileSystem) error { return f.Create(ctx, "/x") }, []string{"/x"}},
		"Mkdir":     {func(ctx context.Context, f vfs.FileSystem) error { return f.Mkdir(ctx, "/x") }, []string{"/x"}},
		"Remove":    {func(ctx context.Context, f vfs.FileSystem) error { return f.Remove(ctx, "/x") }, []string{"/x"}},
		"RemoveAll": {func(ctx context.Context, f vfs.FileSystem) error { return f.RemoveAll(ctx, "/x") }, []string{"/x"}},
		"Write":     {func(ctx context.Context, f vfs.FileSystem) error { return f.Write(ctx, "/x", nil) }, []string{"/x"}},
		"Chmod":     {func(ctx context.Context, f vfs.FileSystem) error { return f.Chmod(ctx, "/x", 0o644) }, []string{"/x"}},
		"Rename": {func(ctx context.Context, f vfs.FileSystem) error {
			return f.Rename(ctx, "/x", "/y")
		}, []string{"/x", "/y"}},
		"Symlink": {func(ctx context.Context, f vfs.FileSystem) error {
			return f.AsSymlinker().Symlink(ctx, "/x", "/y")
		}, []string{"/y"}},
		"Truncate": {func(ctx context.Context, f vfs.FileSystem) error {
			return f.AsTruncater().Truncate(ctx, "/x", 0)
		}, []string{"/x"}},
		"OpenWrite": {func(ctx context.Context, f vfs.FileSystem) error {
			stream, err := f.AsStreamer().OpenWrite(ctx, "/x")
			if err != nil {
				return err
			}
			return stream.Close()
		}, []string{"/x"}},
	}
}

// reading is every call that leaves the tree as it found it. None of them is
// anything an indexer has to hear about.
func reading() map[string]func(context.Context, vfs.FileSystem) error {
	return map[string]func(context.Context, vfs.FileSystem) error{
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
		"Readlink": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := f.AsSymlinker().Readlink(ctx, "/x")
			return err
		},
		"Lstat": func(ctx context.Context, f vfs.FileSystem) error {
			_, err := vfs.Lstat(ctx, f, "/x")
			return err
		},
		"OpenRead": func(ctx context.Context, f vfs.FileSystem) error {
			stream, err := f.AsStreamer().OpenRead(ctx, "/x")
			if err != nil {
				return err
			}
			return stream.Close()
		},
	}
}

// The two lists above are the port's, split in two. A verb added to the port
// arrives in neither and fails here, because whether it changes anything is
// exactly what nobody can be told to remember.
func TestEveryVerbIsEitherAChangeOrNot(t *testing.T) {
	sending, _ := portVerbs()

	classified := append(slices.Collect(maps.Keys(mutating())), slices.Collect(maps.Keys(reading()))...)
	slices.Sort(classified)

	if !slices.Equal(classified, sending) {
		t.Errorf("verbs classified as changing or not = %v, want the port's %v", classified, sending)
	}
}

// The ticket in one test: a write is reported without the caller asking, and it
// is reported as the path it changed.
func TestEveryWriteIsReported(t *testing.T) {
	for name, change := range mutating() {
		t.Run(name, func(t *testing.T) {
			var seen told
			fsys := vfs.WithWriteObserver(writable{}, &seen)

			if err := change.send(t.Context(), fsys); err != nil {
				t.Fatalf("%s = %v", name, err)
			}
			if got := seen.reported(); !slices.Equal(got, change.paths) {
				t.Errorf("%s reported %v, want %v", name, got, change.paths)
			}
		})
	}
}

func TestAReadIsNotReported(t *testing.T) {
	for name, send := range reading() {
		t.Run(name, func(t *testing.T) {
			var seen told
			fsys := vfs.WithWriteObserver(writable{}, &seen)

			if err := send(t.Context(), fsys); err != nil {
				t.Fatalf("%s = %v", name, err)
			}
			if got := seen.reported(); len(got) != 0 {
				t.Errorf("%s reported %v, want nothing", name, got)
			}
		})
	}
}

// A write that failed changed nothing, so there is nothing to index. Reporting
// it would have an indexer read a path over a plugin that has just said it
// could not answer.
func TestAWriteThatFailedIsNotReported(t *testing.T) {
	for name, change := range mutating() {
		t.Run(name, func(t *testing.T) {
			var seen told
			fsys := vfs.WithWriteObserver(writable{refuse: errBroken}, &seen)

			if err := change.send(t.Context(), fsys); !errors.Is(err, errBroken) {
				t.Fatalf("%s of a plugin that refuses = %v, want %v", name, err, errBroken)
			}
			if got := seen.reported(); len(got) != 0 {
				t.Errorf("%s that failed reported %v, want nothing", name, got)
			}
		})
	}
}

// The write happened. An observer that could not be told does not make it
// un-happen, and a caller told otherwise would write it again — twice on an
// append-only path.
func TestAnObserverThatCannotBeToldDoesNotFailTheWrite(t *testing.T) {
	seen := told{refuse: errUnheard}
	fsys := vfs.WithWriteObserver(writable{}, &seen)

	if err := fsys.Write(t.Context(), "/x", nil); err != nil {
		t.Errorf("Write with an observer that fails = %v, want it to succeed", err)
	}
	if got := seen.reported(); !slices.Equal(got, []string{"/x"}) {
		t.Errorf("the observer was told %v, want %v", got, []string{"/x"})
	}
}

// A path a read consumes is a path an indexer cannot be told about, because
// being told would make the indexer the reader. It is also what stops an
// observer that publishes to a queue from feeding itself.
func TestAWriteToAReadDestructivePathIsNotReported(t *testing.T) {
	var seen told
	fsys := vfs.WithWriteObserver(writable{caps: vfs.Caps{ReadDestructive: true}}, &seen)

	if err := fsys.Write(t.Context(), "/jobs/x", nil); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if got := seen.reported(); len(got) != 0 {
		t.Errorf("a write to a queue reported %v, want nothing", got)
	}
}

// A streamed write is one write, and it is not one until Close returns.
func TestAStreamedWriteIsReportedOnceItIsClosed(t *testing.T) {
	var seen told
	fsys := vfs.WithWriteObserver(writable{}, &seen)

	stream, err := fsys.AsStreamer().OpenWrite(t.Context(), "/x")
	if err != nil {
		t.Fatalf("OpenWrite = %v", err)
	}
	if _, err := fmt.Fprint(stream, "half"); err != nil {
		t.Fatalf("write to the stream = %v", err)
	}
	if got := seen.reported(); len(got) != 0 {
		t.Fatalf("an open stream reported %v, want nothing until it is closed", got)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := seen.reported(); !slices.Equal(got, []string{"/x"}) {
		t.Errorf("a closed stream reported %v, want %v", got, []string{"/x"})
	}
}

// An observer says nothing about what a plugin can do: what it refused
// unobserved it refuses observed, so the caller's nil guard keeps telling the
// truth.
func TestAPluginWithNoOptionalVerbsStillHasNone(t *testing.T) {
	var seen told
	fsys := vfs.WithWriteObserver(struct {
		stub
		vfs.NoAccessors
	}{}, &seen)

	if link := fsys.AsSymlinker(); link != nil {
		t.Errorf("AsSymlinker of a plugin with no links = %v, want nil", link)
	}
	if stream := fsys.AsStreamer(); stream != nil {
		t.Errorf("AsStreamer of a plugin with no streams = %v, want nil", stream)
	}
	if cut := fsys.AsTruncater(); cut != nil {
		t.Errorf("AsTruncater of a plugin with no truncate = %v, want nil", cut)
	}
}
