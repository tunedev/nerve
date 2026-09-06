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

// The mount contains a path to the mount's root, which is one root for every
// subtree under it. A link standing in one subtree and pointing at a sibling
// never leaves that root, so the kernel follows it and the mount serves what it
// lands on — and a subtree handed out as a root of its own does not, because
// the kernel is now resolving against the subtree.
//
// This is the boundary a scope stands on. Deriving the name cannot draw it: the
// link is resolved inside the syscall, long after any name was derived.
func TestASubtreeContainsALinkTheMountDoesNot(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"alice", "bob"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("Mkdir(%q) = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bob", "secret"), []byte("not yours"), 0o644); err != nil {
		t.Fatalf("writing bob's file = %v", err)
	}
	if err := os.Symlink("../bob", filepath.Join(root, "alice", "peek")); err != nil {
		t.Fatalf("planting the link = %v", err)
	}

	fsys := mustNew(t, root)
	ctx := context.Background()
	if content, err := fsys.Read(ctx, "/alice/peek/secret"); err != nil || string(content) != "not yours" {
		t.Fatalf("Read through the link from the mount = %q, %v, want it followed", content, err)
	}

	alice := fsys.Sub("/alice")
	if _, err := alice.Read(ctx, "/peek/secret"); err == nil {
		t.Error("Read through the link from the subtree succeeded, want it refused")
	}
	if _, err := alice.Stat(ctx, "/peek"); err == nil {
		t.Error("Stat through the link from the subtree succeeded, want it refused")
	}
	if _, err := alice.ReadDir(ctx, "/peek"); err == nil {
		t.Error("ReadDir through the link from the subtree succeeded, want it refused")
	}
}

// A subtree is a value a caller takes per request, so it must not be a
// descriptor a caller has no way to give back. The handle is taken per verb and
// closed again, and this is what says so.
func TestASubtreeHoldsNoDescriptorBetweenVerbs(t *testing.T) {
	open, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd to count descriptors in: %v", err)
	}

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "alice"), 0o755); err != nil {
		t.Fatalf("Mkdir = %v", err)
	}
	fsys := mustNew(t, root).Sub("/alice")
	ctx := context.Background()
	if err := fsys.Write(ctx, "/notes", []byte("text")); err != nil {
		t.Fatalf("Write = %v", err)
	}
	for range 200 {
		if _, err := fsys.Read(ctx, "/notes"); err != nil {
			t.Fatalf("Read = %v", err)
		}
	}

	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("counting descriptors after the reads = %v", err)
	}
	if len(after) > len(open)+1 {
		t.Errorf("descriptors went from %d to %d over 200 reads through a subtree, want them given back", len(open), len(after))
	}
}

// A subtree stands on the directory named, not on whatever a link at that name
// stands for. os.Root follows a link that stays inside it, so opening the
// scope path in one call hands out the link's target as the subtree's own root
// — and the target is another user's directory whenever anything with write
// access to the mount planted one, which for a passthrough plugin is the
// ordinary condition.
//
// Both cases are here because a link is a link wherever it stands: the last
// component of the scope path and any component above it escape alike.
func TestASubtreePathThroughALinkIsNotAnotherUsersSubtree(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, root string){
		"the last component is a link": func(t *testing.T, root string) {
			mustMkdirHost(t, filepath.Join(root, "alice"))
			mustSymlinkHost(t, "../bob/cv", filepath.Join(root, "alice", "cv"))
		},
		"a component above it is a link": func(t *testing.T, root string) {
			mustSymlinkHost(t, "bob", filepath.Join(root, "alice"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mustMkdirHost(t, filepath.Join(root, "bob", "cv"))
			if err := os.WriteFile(filepath.Join(root, "bob", "cv", "secret"), []byte("bob's"), 0o644); err != nil {
				t.Fatalf("writing bob's file = %v", err)
			}
			plant(t, root)

			fsys := mustNew(t, root)
			ctx := context.Background()
			// The positive control: the link is live and leads where the case
			// says it does, so a refusal below is the subtree refusing rather
			// than a world with nothing in it.
			if content, err := fsys.Read(ctx, "/alice/cv/secret"); err != nil || string(content) != "bob's" {
				t.Fatalf("Read through the link from the mount = %q, %v, want bob's content", content, err)
			}

			alice := fsys.Sub("/alice/cv")
			if content, err := alice.Read(ctx, "/secret"); err == nil {
				t.Errorf("Read from the subtree = %q, want the link refused", content)
			}
			if found, err := alice.Stat(ctx, "/secret"); err == nil {
				t.Errorf("Stat from the subtree = %+v, want the link refused", found)
			}
			if entries, err := alice.ReadDir(ctx, "/"); err == nil {
				t.Errorf("ReadDir from the subtree = %v, want the link refused", entries)
			}
			if err := alice.Write(ctx, "/planted", []byte("alice was here")); err == nil {
				t.Error("Write from the subtree succeeded, want the link refused")
			}
			if _, err := os.Lstat(filepath.Join(root, "bob", "cv", "planted")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("bob's directory holds the written entry = %v, want nothing written into it", err)
			}
		})
	}
}

// Making the subtree is the one verb that reaches the scope path before the
// subtree is open, so it is the one that could write into another user's
// directory rather than only read from it: a component that is not there is
// created where the mount holds it, never where a link says.
func TestMakingASubtreeThroughALinkCreatesNothingInAnothersSubtree(t *testing.T) {
	root := t.TempDir()
	mustMkdirHost(t, filepath.Join(root, "bob"))
	mustSymlinkHost(t, "bob", filepath.Join(root, "alice"))

	if err := mustNew(t, root).Sub("/alice/cv").Mkdir(context.Background(), "/drafts"); err == nil {
		t.Error("Mkdir through the link succeeded, want it refused")
	}

	if _, err := os.Lstat(filepath.Join(root, "bob", "cv")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("bob's directory holds a made subtree = %v, want nothing made in it", err)
	}
}

// A subtree whose path holds no link is served, which is what says the refusal
// above is about the link and not about subtrees.
func TestASubtreeOfPlainDirectoriesIsServed(t *testing.T) {
	root := t.TempDir()
	mustMkdirHost(t, filepath.Join(root, "alice", "cv"))

	alice := mustNew(t, root).Sub("/alice/cv")
	ctx := context.Background()
	if err := alice.Write(ctx, "/own.md", []byte("alice's draft")); err != nil {
		t.Fatalf("Write = %v", err)
	}
	content, err := alice.Read(ctx, "/own.md")
	if err != nil || string(content) != "alice's draft" {
		t.Fatalf("Read = %q, %v, want alice's draft", content, err)
	}
	if err := alice.Mkdir(ctx, "/drafts"); err != nil {
		t.Errorf("Mkdir = %v", err)
	}
}

func mustMkdirHost(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) = %v", dir, err)
	}
}

func mustSymlinkHost(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("planting the link %q -> %q = %v", link, target, err)
	}
}
