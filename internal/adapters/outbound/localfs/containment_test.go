package localfs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
)

// A mount is a subtree and nothing above it is reachable, however the path is
// spelled. Cross-user isolation is a correctness requirement here, not
// hardening: a path that climbs out of the root would read another workspace.
func TestNoPathClimbsOutOfTheRoot(t *testing.T) {
	above := t.TempDir()
	if err := os.WriteFile(filepath.Join(above, "secret"), []byte("not yours"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}
	root := filepath.Join(above, "mount")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir(%q) = %v", root, err)
	}

	fsys := mustNew(t, root)
	ctx := context.Background()
	for _, name := range []string{"/../secret", "../secret", "/nested/../../secret", "/..", "/../"} {
		if _, err := fsys.Read(ctx, name); !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Read(%q) = %v, want it to stay inside the mount", name, err)
		}
	}

	if _, err := os.Stat(filepath.Join(above, "secret")); err != nil {
		t.Errorf("the file above the root = %v, want it untouched", err)
	}
}

// A symlink is resolved by the kernel at the syscall, long after any path has
// been cleaned, so lexical containment cannot see one. Anything that can write
// into the root can plant it - another process, another user, an extracted
// archive - which for a plugin whose whole purpose is passthrough onto a host
// directory is the normal condition rather than an exotic one.
func TestNoHostPlantedSymlinkLeadsOutOfTheRoot(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("not yours"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("planting the symlink = %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "straight-out")); err != nil {
		t.Fatalf("planting the symlink = %v", err)
	}

	fsys := mustNew(t, root)
	ctx := context.Background()
	for _, name := range []string{"/escape/secret", "/straight-out"} {
		content, err := fsys.Read(ctx, name)
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Read(%q) = %q, %v, want fs.ErrPermission", name, content, err)
		}
	}

	if err := fsys.Write(ctx, "/escape/planted", []byte("mine now")); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Write through a link out of the mount = %v, want fs.ErrPermission", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file outside the root = %v, want it never written", err)
	}
}

// Emptying the root deletes the link and not what it stands for. RemoveAll is
// the one verb where following a planted link would destroy another workspace
// rather than read one.
func TestEmptyingTheRootDeletesALinkAndNotItsTarget(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("not yours"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("planting the symlink = %v", err)
	}

	if err := mustNew(t, root).RemoveAll(context.Background(), "/"); err != nil {
		t.Fatalf("RemoveAll(/) = %v", err)
	}

	if _, err := os.Lstat(filepath.Join(root, "escape")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link inside the root = %v, want it deleted", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Errorf("the file outside the root = %v, want it untouched", err)
	}
}

// A link made through the port is followed through the port. Containment that
// refused those as well would be a mount whose own links do not work.
func TestALinkMadeThroughThePortIsFollowedThroughIt(t *testing.T) {
	fsys := mustNew(t, t.TempDir())
	ctx := context.Background()
	if err := fsys.Mkdir(ctx, "/notes/deep"); err != nil {
		t.Fatalf("Mkdir(/notes/deep) = %v", err)
	}
	if err := fsys.Write(ctx, "/notes/deep/text", []byte("inside")); err != nil {
		t.Fatalf("Write(/notes/deep/text) = %v", err)
	}
	if err := fsys.Mkdir(ctx, "/links"); err != nil {
		t.Fatalf("Mkdir(/links) = %v", err)
	}
	if err := fsys.AsSymlinker().Symlink(ctx, "/notes/deep/text", "/links/shortcut"); err != nil {
		t.Fatalf("Symlink(/notes/deep/text, /links/shortcut) = %v", err)
	}

	content, err := fsys.Read(ctx, "/links/shortcut")
	if err != nil {
		t.Fatalf("Read(/links/shortcut) = %v", err)
	}
	if string(content) != "inside" {
		t.Errorf("Read(/links/shortcut) = %q, want %q", content, "inside")
	}

	target, err := fsys.AsSymlinker().Readlink(ctx, "/links/shortcut")
	if err != nil {
		t.Fatalf("Readlink(/links/shortcut) = %v", err)
	}
	if target != "/notes/deep/text" {
		t.Errorf("Readlink(/links/shortcut) = %q, want %q", target, "/notes/deep/text")
	}
}

// mustNew is the plugin over a host directory, for a case that has nothing to
// say about a root that will not open.
func mustNew(t *testing.T, root string) *localfs.FS {
	t.Helper()
	fsys, err := localfs.New(root)
	if err != nil {
		t.Fatalf("New(%q) = %v", root, err)
	}
	return fsys
}
