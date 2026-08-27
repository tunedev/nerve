package queuefs_test

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// queuefs answers the port a mount table dispatches to, which is the only
// reason it can be mounted at all.
var _ vfs.FileSystem = queuefs.New("test", nil, nil)

// The descriptor is the path and not the kind. One mount consumes what it hands
// back under the queue and refuses to be written one path up, and no type could
// have said both: this is the case the whole per-path descriptor exists for.
func TestCapabilitiesVaryWithinTheMount(t *testing.T) {
	fsys := newQueue(t)

	queued := vfs.Caps{ReadDestructive: true, ObjectStore: true}
	for _, name := range []string{"/jobs", "/jobs/next", "/jobs/deep/absent"} {
		if got := fsys.Capabilities(name); got != queued {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, got, queued)
		}
	}

	fixed := vfs.Caps{ReadOnly: true}
	for _, name := range []string{"/", "/config", "/elsewhere"} {
		if got := fsys.Capabilities(name); got != fixed {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, got, fixed)
		}
	}
}

// The config path describes the queue and is read rather than consumed, which
// is what makes it a separate path from the one that is.
func TestTheConfigPathIsReadAgain(t *testing.T) {
	fsys := newQueue(t)

	first := mustRead(t, fsys, "/config")
	if first != "queue test\n" {
		t.Fatalf("Read(/config) = %q, want the queue's name", first)
	}
	if again := mustRead(t, fsys, "/config"); again != first {
		t.Errorf("Read(/config) twice = %q then %q, want one answer", first, again)
	}
}

// The mount's own shape is not a caller's to rearrange. Writing beside the two
// paths it holds is refused rather than quietly creating a third.
func TestTheMountsShapeIsRefused(t *testing.T) {
	fsys := newQueue(t)
	ctx := t.Context()

	refusals := map[string]error{
		"write outside":  fsys.Write(ctx, "/elsewhere", []byte("x")),
		"write config":   fsys.Write(ctx, "/config", []byte("x")),
		"mkdir the jobs": fsys.Mkdir(ctx, "/jobs"),
		"remove config":  fsys.Remove(ctx, "/config"),
		"rename config":  fsys.Rename(ctx, "/config", "/jobs/config"),
		"chmod the jobs": fsys.Chmod(ctx, "/jobs", 0o700),
	}
	for _, what := range slices.Sorted(maps.Keys(refusals)) {
		if !errors.Is(refusals[what], fs.ErrPermission) {
			t.Errorf("%s = %v, want fs.ErrPermission", what, refusals[what])
		}
	}
}

// A message name is a key, so a prefix of one is a directory for as long as a
// message sits under it and nothing has to be made first.
func TestPrefixesUnderTheQueue(t *testing.T) {
	fsys := newQueue(t)
	mustWrite(t, fsys, "/jobs/index/document/42", "work")

	info, err := fsys.Stat(t.Context(), "/jobs/index")
	if err != nil || !info.IsDir() {
		t.Fatalf("Stat(/jobs/index) = %+v, %v, want a directory", info, err)
	}

	entries, err := fsys.ReadDir(t.Context(), "/jobs/index")
	if err != nil {
		t.Fatalf("ReadDir(/jobs/index) = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "document" || !entries[0].IsDir() {
		t.Fatalf("ReadDir(/jobs/index) = %+v, want one directory named document", entries)
	}

	if content := mustRead(t, fsys, "/jobs/index/document/42"); content != "work" {
		t.Errorf("Read = %q, want %q", content, "work")
	}
	if _, err := fsys.Stat(t.Context(), "/jobs/index"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(/jobs/index) after the last message under it was consumed = %v, want fs.ErrNotExist", err)
	}
}

// The mount root lists the shape rather than the queue, so a caller that walks
// in from above finds the two places without knowing they were coming.
func TestTheRootListsTheShape(t *testing.T) {
	fsys := newQueue(t)

	entries, err := fsys.ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}
	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	if !slices.Equal(listed, []string{queuefs.Config, queuefs.Jobs}) {
		t.Errorf("ReadDir(/) = %v, want %v", listed, []string{queuefs.Config, queuefs.Jobs})
	}
}

func mustWrite(t *testing.T, fsys vfs.FileSystem, name, content string) {
	t.Helper()
	if err := fsys.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func mustRead(t *testing.T, fsys vfs.FileSystem, name string) string {
	t.Helper()
	content, err := fsys.Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%q) = %v", name, err)
	}
	return string(content)
}

// The descriptor has to survive the dispatcher. Every caller holds the mount
// table rather than the plugin, so a queue that reported read-destructive only
// to whoever held it directly would report nothing to anyone real.
func TestTheMountTableForwardsThePerPathDescriptor(t *testing.T) {
	table := vfs.NewMountTable()
	if err := table.Mount("/queue", newQueue(t)); err != nil {
		t.Fatalf("Mount = %v", err)
	}

	if got := table.Capabilities("/queue/jobs/next"); !got.ReadDestructive {
		t.Errorf("Capabilities(/queue/jobs/next) through the table = %+v, want read-destructive", got)
	}
	if got := table.Capabilities("/queue/config"); got.ReadDestructive {
		t.Errorf("Capabilities(/queue/config) through the table = %+v, want it not read-destructive", got)
	}
}
