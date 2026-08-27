package proxyfs_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/devfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/proxyfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// A proxy renames: what a caller writes at one name lands in the target at
// another, and nowhere else.
func TestAWriteLandsUnderThePrefix(t *testing.T) {
	target := memfs.New()
	if err := target.Mkdir(t.Context(), "/sub"); err != nil {
		t.Fatalf("Mkdir(/sub) = %v", err)
	}
	fsys := proxyfs.New(target, "/sub")

	if err := fsys.Write(t.Context(), "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) through the proxy = %v", err)
	}

	content, err := target.Read(t.Context(), "/sub/notes")
	if err != nil {
		t.Fatalf("Read(/sub/notes) from the target = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("the target holds %q at /sub/notes, want %q", content, "text")
	}
	if _, err := target.Read(t.Context(), "/notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/notes) from the target = %v, want nothing outside the subtree", err)
	}
}

// Stat at the root names the proxy's own root, because a caller that mounted a
// proxy should not have to know what it was pointed at to read a name back.
func TestStatOfTheRootNamesTheProxy(t *testing.T) {
	target := memfs.New()
	if err := target.Mkdir(t.Context(), "/sub"); err != nil {
		t.Fatalf("Mkdir(/sub) = %v", err)
	}

	info, err := proxyfs.New(target, "/sub").Stat(t.Context(), "/")
	if err != nil {
		t.Fatalf("Stat(/) = %v", err)
	}
	if info.Name != "/" {
		t.Errorf("Stat(/).Name = %q, want %q", info.Name, "/")
	}
	if !info.IsDir() {
		t.Error("Stat(/).IsDir() = false, want a directory")
	}
}

// The subtree is the ground the proxy stands on. Removing or moving it would
// leave a mount answering nothing, so both are refused where the target would
// have allowed them.
func TestTheRootIsNotTheProxysToRemove(t *testing.T) {
	target := memfs.New()
	if err := target.Mkdir(t.Context(), "/sub"); err != nil {
		t.Fatalf("Mkdir(/sub) = %v", err)
	}
	fsys := proxyfs.New(target, "/sub")

	if err := fsys.Remove(t.Context(), "/"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Remove(/) = %v, want fs.ErrInvalid", err)
	}
	if err := fsys.Rename(t.Context(), "/", "/elsewhere"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Rename(/) = %v, want fs.ErrInvalid", err)
	}

	if _, err := target.Stat(t.Context(), "/sub"); err != nil {
		t.Errorf("the subtree after the refusals = %v, want it still there", err)
	}
}

// RemoveAll at the root empties the subtree without deleting it, which is what
// keeps the mount answerable afterwards.
func TestRemoveAllOfTheRootEmptiesTheSubtree(t *testing.T) {
	target := memfs.New()
	if err := target.Mkdir(t.Context(), "/sub"); err != nil {
		t.Fatalf("Mkdir(/sub) = %v", err)
	}
	fsys := proxyfs.New(target, "/sub")
	if err := fsys.Write(t.Context(), "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}

	if err := fsys.RemoveAll(t.Context(), "/"); err != nil {
		t.Fatalf("RemoveAll(/) = %v", err)
	}

	entries, err := fsys.ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) after RemoveAll = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/) = %d entries, want none", len(entries))
	}
	if _, err := target.Stat(t.Context(), "/sub"); err != nil {
		t.Errorf("the subtree after RemoveAll = %v, want it still there", err)
	}
}

// A proxy is not a thing with capabilities of its own. It claims what stands
// behind it claims, at the name that stands for the one it was asked about,
// which is why the descriptor is forwarded per path rather than answered once.
func TestCapabilitiesComeFromTheTargetPerPath(t *testing.T) {
	fsys := proxyfs.New(devfs.New(16), "/")

	if caps := fsys.Capabilities("/null"); caps.ReadOnly {
		t.Error("Capabilities(/null) through the proxy = read-only, want the target's answer")
	}
	if caps := fsys.Capabilities("/zero"); !caps.ReadOnly {
		t.Error("Capabilities(/zero) through the proxy = writable, want the target's answer")
	}
}

// A caller holding the proxy and reaching for an optional verb set gets the
// target's, or nothing when the target has none. This is what the accessors on
// the base interface exist for: the assertion a caller would otherwise write
// interrogates the proxy and never the plugin that would do the work.
func TestAccessorsAnswerForTheTarget(t *testing.T) {
	var fsys vfs.FileSystem = proxyfs.New(memfs.New(), "/")

	if fsys.AsSymlinker() != nil {
		t.Error("AsSymlinker() = a verb set, want nil for a target with no link verbs")
	}
	if fsys.AsStreamer() == nil {
		t.Error("AsStreamer() = nil, want the target's streaming verbs")
	}
	if fsys.AsTruncater() == nil {
		t.Error("AsTruncater() = nil, want the target's truncate verb")
	}
}

func TestFactoryNeedsAKindBehindIt(t *testing.T) {
	build := func(vfs.MountSpec) (vfs.FileSystem, error) { return memfs.New(), nil }

	if _, err := proxyfs.Factory(build)(nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory()(nil) = %v, want fs.ErrInvalid", err)
	}
}

// A config naming a proxy behind a proxy would build until the stack ran out,
// and reaches nothing a proxy over its target would not.
func TestFactoryRefusesAProxyBehindAProxy(t *testing.T) {
	build := func(vfs.MountSpec) (vfs.FileSystem, error) { return memfs.New(), nil }

	settings := map[string]string{"target_kind": "proxyfs"}
	if _, err := proxyfs.Factory(build)(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory()(target_kind=proxyfs) = %v, want fs.ErrInvalid", err)
	}
}

// Everything but the two settings a proxy reads for itself belongs to the kind
// behind it, and reaches it unchanged.
func TestFactoryPassesTheRestThrough(t *testing.T) {
	var built vfs.MountSpec
	build := func(spec vfs.MountSpec) (vfs.FileSystem, error) {
		built = spec
		return memfs.New(), nil
	}

	settings := map[string]string{"target_kind": "localfs", "target_prefix": "/sub", "root": "/srv"}
	if _, err := proxyfs.Factory(build)(settings); err != nil {
		t.Fatalf("Factory()(localfs) = %v", err)
	}

	if built.Kind != "localfs" {
		t.Errorf("the target kind = %q, want %q", built.Kind, "localfs")
	}
	if built.Settings["root"] != "/srv" {
		t.Errorf("the target settings = %v, want root passed through", built.Settings)
	}
	for _, mine := range []string{"target_kind", "target_prefix"} {
		if _, passed := built.Settings[mine]; passed {
			t.Errorf("the target was given %q, want the proxy's own settings kept back", mine)
		}
	}
}
