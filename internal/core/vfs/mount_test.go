package vfs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// recorder remembers what the dispatcher handed it, so a test can see both
// which plugin was resolved and what the path had become by the time it
// arrived.
type recorder struct {
	vfs.NoAccessors
	name  string
	verb  string
	path  string
	other string
}

func (r *recorder) saw(verb, path string) { r.verb, r.path = verb, path }

func (r *recorder) Create(_ context.Context, path string) error { r.saw("Create", path); return nil }
func (r *recorder) Mkdir(_ context.Context, path string) error  { r.saw("Mkdir", path); return nil }
func (r *recorder) Remove(_ context.Context, path string) error { r.saw("Remove", path); return nil }
func (r *recorder) RemoveAll(_ context.Context, path string) error {
	r.saw("RemoveAll", path)
	return nil
}

func (r *recorder) Read(_ context.Context, path string) ([]byte, error) {
	r.saw("Read", path)
	return []byte(r.name), nil
}

func (r *recorder) Write(_ context.Context, path string, _ []byte) error {
	r.saw("Write", path)
	return nil
}

func (r *recorder) ReadDir(_ context.Context, path string) ([]vfs.FileInfo, error) {
	r.saw("ReadDir", path)
	return nil, nil
}

func (r *recorder) Stat(_ context.Context, path string) (vfs.FileInfo, error) {
	r.saw("Stat", path)
	return vfs.FileInfo{Name: r.name}, nil
}

func (r *recorder) Rename(_ context.Context, oldPath, newPath string) error {
	r.saw("Rename", oldPath)
	r.other = newPath
	return nil
}

func (r *recorder) Chmod(_ context.Context, path string, _ fs.FileMode) error {
	r.saw("Chmod", path)
	return nil
}

// Capabilities reports read-destructive under /jobs, so a test can tell a
// forwarded descriptor from an invented one.
func (r *recorder) Capabilities(path string) vfs.Caps {
	r.path = path
	return vfs.Caps{ReadDestructive: path == "/jobs/next"}
}

func mounted(t *testing.T, prefixes ...string) (*vfs.MountTable, map[string]*recorder) {
	t.Helper()

	table := vfs.NewMountTable()
	plugins := make(map[string]*recorder, len(prefixes))
	for _, prefix := range prefixes {
		plugins[prefix] = &recorder{name: prefix}
		if err := table.Mount(prefix, plugins[prefix]); err != nil {
			t.Fatalf("Mount(%q) = %v, want nil", prefix, err)
		}
	}
	return table, plugins
}

var _ vfs.FileSystem = (*vfs.MountTable)(nil)

// The deepest mount holding a path is the one that gets it, whatever order the
// mounts were added in.
func TestLongestPrefixWins(t *testing.T) {
	table, plugins := mounted(t, "/queue", "/", "/queue/jobs")

	if _, err := table.Stat(context.Background(), "/queue/jobs/next"); err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}

	if got := plugins["/queue/jobs"].path; got != "/next" {
		t.Errorf("/queue/jobs mount saw %q, want %q", got, "/next")
	}
	if plugins["/queue"].verb != "" || plugins["/"].verb != "" {
		t.Error("a shallower mount was handed the path")
	}
}

// A prefix names a subtree, not a string. /queue must not swallow /queuebase.
func TestPrefixMatchesOnlyAtASegmentBoundary(t *testing.T) {
	table, plugins := mounted(t, "/", "/queue")

	if _, err := table.Stat(context.Background(), "/queuebase/thing"); err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}

	if plugins["/queue"].verb != "" {
		t.Error("/queue mount claimed /queuebase/thing")
	}
	if got := plugins["/"].path; got != "/queuebase/thing" {
		t.Errorf("root mount saw %q, want %q", got, "/queuebase/thing")
	}
}

// The mount point itself is a path the plugin owns, and it is that plugin's root.
func TestMountPointResolvesToThePluginRoot(t *testing.T) {
	table, plugins := mounted(t, "/queue")

	if _, err := table.ReadDir(context.Background(), "/queue"); err != nil {
		t.Fatalf("ReadDir() = %v, want nil", err)
	}

	if got := plugins["/queue"].path; got != "/" {
		t.Errorf("mount saw %q, want %q", got, "/")
	}
}

// Every verb has to forward, not just the ones a first pass remembered.
func TestEveryVerbForwardsTheTranslatedPath(t *testing.T) {
	ctx := context.Background()
	const full, rest = "/queue/jobs/next", "/jobs/next"

	verbs := []struct {
		name string
		call func(vfs.FileSystem) error
	}{
		{"Create", func(f vfs.FileSystem) error { return f.Create(ctx, full) }},
		{"Mkdir", func(f vfs.FileSystem) error { return f.Mkdir(ctx, full) }},
		{"Remove", func(f vfs.FileSystem) error { return f.Remove(ctx, full) }},
		{"RemoveAll", func(f vfs.FileSystem) error { return f.RemoveAll(ctx, full) }},
		{"Read", func(f vfs.FileSystem) error { _, err := f.Read(ctx, full); return err }},
		{"Write", func(f vfs.FileSystem) error { return f.Write(ctx, full, nil) }},
		{"ReadDir", func(f vfs.FileSystem) error { _, err := f.ReadDir(ctx, full); return err }},
		{"Stat", func(f vfs.FileSystem) error { _, err := f.Stat(ctx, full); return err }},
		{"Rename", func(f vfs.FileSystem) error { return f.Rename(ctx, full, "/queue/jobs/done") }},
		{"Chmod", func(f vfs.FileSystem) error { return f.Chmod(ctx, full, 0o644) }},
	}

	for _, verb := range verbs {
		t.Run(verb.name, func(t *testing.T) {
			table, plugins := mounted(t, "/queue")

			if err := verb.call(table); err != nil {
				t.Fatalf("%s() = %v, want nil", verb.name, err)
			}

			plugin := plugins["/queue"]
			if plugin.verb != verb.name || plugin.path != rest {
				t.Errorf("mount saw %s(%q), want %s(%q)", plugin.verb, plugin.path, verb.name, rest)
			}
		})
	}
}

func TestRenameTranslatesBothPaths(t *testing.T) {
	table, plugins := mounted(t, "/queue")

	if err := table.Rename(context.Background(), "/queue/jobs/next", "/queue/jobs/done"); err != nil {
		t.Fatalf("Rename() = %v, want nil", err)
	}

	plugin := plugins["/queue"]
	if plugin.path != "/jobs/next" || plugin.other != "/jobs/done" {
		t.Errorf("mount saw Rename(%q, %q), want Rename(%q, %q)",
			plugin.path, plugin.other, "/jobs/next", "/jobs/done")
	}
}

// No plugin can move an entry it does not hold, so the table refuses rather
// than inventing a copy-and-delete the caller did not ask for.
func TestRenameAcrossMountsIsRefused(t *testing.T) {
	table, _ := mounted(t, "/queue", "/store")

	err := table.Rename(context.Background(), "/queue/jobs/next", "/store/next")

	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename() = %v, want fs.ErrInvalid", err)
	}
}

func TestPathNoMountHoldsDoesNotExist(t *testing.T) {
	table, _ := mounted(t, "/queue")

	_, err := table.Stat(context.Background(), "/elsewhere")

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat() = %v, want fs.ErrNotExist", err)
	}
}

// Descriptors are the half of discovery that is data, and the plugin has to be
// asked about its own path, not the table's.
func TestDescriptorsComeFromTheResolvedPlugin(t *testing.T) {
	table, _ := mounted(t, "/queue")

	if !table.Capabilities("/queue/jobs/next").ReadDestructive {
		t.Error("/queue/jobs/next does not report the plugin's ReadDestructive")
	}
	if table.Capabilities("/queue/config").ReadDestructive {
		t.Error("/queue/config reports ReadDestructive")
	}
}

func TestUnmountedPathClaimsNoCapability(t *testing.T) {
	table, _ := mounted(t, "/queue")

	if caps := table.Capabilities("/elsewhere"); caps != (vfs.Caps{}) {
		t.Errorf("Capabilities() = %+v, want the zero value", caps)
	}
}

// linking is a mount whose plugin answers one accessor. Its Readlink returns a
// path in its own vocabulary, which the table has to put back into the table's.
type linking struct {
	recorder
}

func (l *linking) AsSymlinker() vfs.Symlinker { return l }

func (l *linking) Symlink(_ context.Context, oldPath, newPath string) error {
	l.saw("Symlink", oldPath)
	l.other = newPath
	return nil
}

func (l *linking) Readlink(_ context.Context, path string) (string, error) {
	l.saw("Readlink", path)
	return "/jobs/first", nil
}

func linkingTable(t *testing.T) (*vfs.MountTable, *linking) {
	t.Helper()

	table := vfs.NewMountTable()
	plugin := &linking{recorder{name: "/queue"}}
	if err := table.Mount("/queue", plugin); err != nil {
		t.Fatalf("Mount() = %v, want nil", err)
	}
	if err := table.Mount("/plain", &recorder{name: "/plain"}); err != nil {
		t.Fatalf("Mount() = %v, want nil", err)
	}
	return table, plugin
}

func TestAccessorsForwardToTheResolvedPlugin(t *testing.T) {
	table, plugin := linkingTable(t)

	link := table.AsSymlinker()
	if link == nil {
		t.Fatal("AsSymlinker() = nil, want the dispatcher")
	}

	if err := link.Symlink(context.Background(), "/queue/jobs/first", "/queue/jobs/latest"); err != nil {
		t.Fatalf("Symlink() = %v, want nil", err)
	}
	if plugin.path != "/jobs/first" || plugin.other != "/jobs/latest" {
		t.Errorf("mount saw Symlink(%q, %q), want Symlink(%q, %q)",
			plugin.path, plugin.other, "/jobs/first", "/jobs/latest")
	}
}

// A path the plugin hands back is in the plugin's vocabulary, and the caller
// only ever speaks the table's.
func TestReadlinkReturnsATablePath(t *testing.T) {
	table, _ := linkingTable(t)

	target, err := table.AsSymlinker().Readlink(context.Background(), "/queue/jobs/latest")
	if err != nil {
		t.Fatalf("Readlink() = %v, want nil", err)
	}

	if target != "/queue/jobs/first" {
		t.Errorf("Readlink() = %q, want %q", target, "/queue/jobs/first")
	}
}

// The accessor guard answers for the table, so it stays true where it can: a
// mount that refuses the verb set says so per path, as an error.
func TestAccessorRefusesAPathWhoseMountHasNoSuchVerbs(t *testing.T) {
	table, _ := linkingTable(t)

	err := table.AsSymlinker().Symlink(context.Background(), "/plain/a", "/plain/b")

	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Symlink() = %v, want fs.ErrInvalid", err)
	}
}

func TestTableRefusesAnAccessorNoMountAnswers(t *testing.T) {
	table, _ := mounted(t, "/queue", "/store")

	if s := table.AsSymlinker(); s != nil {
		t.Errorf("AsSymlinker() = %v, want nil", s)
	}
	if s := table.AsStreamer(); s != nil {
		t.Errorf("AsStreamer() = %v, want nil", s)
	}
	if cut := table.AsTruncater(); cut != nil {
		t.Errorf("AsTruncater() = %v, want nil", cut)
	}
}

// streaming and truncating mounts, to prove the other two accessors dispatch
// the same way rather than only the one that was written first.
type streamingMount struct {
	recorder
	opened string
}

func (s *streamingMount) AsStreamer() vfs.Streamer { return s }
func (s *streamingMount) AsSubber() vfs.Subber     { return nil }

func (s *streamingMount) OpenRead(_ context.Context, path string) (io.ReadCloser, error) {
	s.opened = path
	return io.NopCloser(strings.NewReader("")), nil
}

func (s *streamingMount) OpenWrite(_ context.Context, path string) (io.WriteCloser, error) {
	s.opened = path
	return nil, nil
}

type truncatingMount struct {
	recorder
	cutTo int64
}

func (c *truncatingMount) AsTruncater() vfs.Truncater { return c }
func (c *truncatingMount) AsSubber() vfs.Subber       { return nil }

func (c *truncatingMount) Truncate(_ context.Context, path string, size int64) error {
	c.path, c.cutTo = path, size
	return nil
}

func TestStreamerAndTruncaterDispatchToo(t *testing.T) {
	table := vfs.NewMountTable()
	stream := &streamingMount{recorder: recorder{name: "/big"}}
	cut := &truncatingMount{recorder: recorder{name: "/cut"}}
	if err := table.Mount("/big", stream); err != nil {
		t.Fatalf("Mount() = %v, want nil", err)
	}
	if err := table.Mount("/cut", cut); err != nil {
		t.Fatalf("Mount() = %v, want nil", err)
	}

	if _, err := table.AsStreamer().OpenRead(context.Background(), "/big/blob"); err != nil {
		t.Fatalf("OpenRead() = %v, want nil", err)
	}
	if stream.opened != "/blob" {
		t.Errorf("stream mount saw OpenRead(%q), want %q", stream.opened, "/blob")
	}

	if err := table.AsTruncater().Truncate(context.Background(), "/cut/entry", 7); err != nil {
		t.Fatalf("Truncate() = %v, want nil", err)
	}
	if cut.path != "/entry" || cut.cutTo != 7 {
		t.Errorf("truncate mount saw Truncate(%q, %d), want Truncate(%q, %d)", cut.path, cut.cutTo, "/entry", 7)
	}
}

func TestMountRejectsAPrefixThatIsNotARootedCleanPath(t *testing.T) {
	for _, prefix := range []string{"", "queue", "/queue/", "/queue/../store", "//queue"} {
		if err := vfs.NewMountTable().Mount(prefix, &recorder{}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Mount(%q) = %v, want fs.ErrInvalid", prefix, err)
		}
	}
}

func TestMountRejectsAPrefixAlreadyTaken(t *testing.T) {
	table, _ := mounted(t, "/queue")

	if err := table.Mount("/queue", &recorder{}); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Mount() = %v, want fs.ErrExist", err)
	}
}

// A caller's path is resolved lexically before it is matched, so no . or ..
// element can land a verb on a mount that does not hold the path it names.
func TestPathsResolveLexicallyBeforeMatching(t *testing.T) {
	table, plugins := mounted(t, "/queue", "/store")

	if _, err := table.Stat(context.Background(), "/queue/../store/thing"); err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}

	if plugins["/queue"].verb != "" {
		t.Error("/queue mount was handed a path that climbs out of it")
	}
	if got := plugins["/store"].path; got != "/thing" {
		t.Errorf("/store mount saw %q, want %q", got, "/thing")
	}
}

// silent answers Stat without recording it, so eight readers can share one
// plugin without racing on the recorder's memory of the last call.
type silent struct{ *recorder }

func (silent) Stat(context.Context, string) (vfs.FileInfo, error) { return vfs.FileInfo{}, nil }

// TestMountsAddedWhileTheTableIsBeingReadAreSafe holds the table to its promise
// of runtime-addable mounts: a mount arriving does not tear the tree out from
// under a reader. Run under -race, which is where the failure shows.
func TestMountsAddedWhileTheTableIsBeingReadAreSafe(t *testing.T) {
	table := vfs.NewMountTable()
	if err := table.Mount("/", silent{&recorder{name: "root"}}); err != nil {
		t.Fatalf("mount root: %v", err)
	}

	const arrivals = 64
	var readers, writers sync.WaitGroup
	stop := make(chan struct{})

	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := table.Stat(context.Background(), "/seg7/deep/leaf"); err != nil {
						t.Errorf("stat while mounting: %v", err)
						return
					}
				}
			}
		}()
	}

	for i := range arrivals {
		writers.Add(1)
		go func() {
			defer writers.Done()
			prefix := fmt.Sprintf("/seg%d", i)
			if err := table.Mount(prefix, silent{&recorder{name: prefix}}); err != nil {
				t.Errorf("mount %q: %v", prefix, err)
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()
}
