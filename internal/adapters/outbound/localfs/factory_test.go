package localfs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestFactoryBuildsATreeOverTheDirectoryItIsGiven(t *testing.T) {
	root := t.TempDir()
	fsys, err := localfs.Factory(map[string]string{"root": root})
	if err != nil {
		t.Fatalf("Factory(root) = %v", err)
	}

	if err := fsys.Write(context.Background(), "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "notes"))
	if err != nil {
		t.Fatalf("reading the host file = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("the host file = %q, want %q", content, "text")
	}
}

// The disk is the tree. What another process wrote into the directory is what a
// caller reads back, which is the whole of what this kind is for.
func TestWhatTheHostWroteIsWhatIsRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes"), []byte("written outside"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}

	content, err := localfs.New(root).Read(context.Background(), "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if string(content) != "written outside" {
		t.Errorf("Read(/notes) = %q, want %q", content, "written outside")
	}
}

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

	fsys := localfs.New(root)
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

func TestFactoryNeedsARoot(t *testing.T) {
	_, err := localfs.Factory(nil)

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(nil) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("Factory(nil) = %v, want the setting named", err)
	}
}

// A root that is not there is a typo an operator can still act on at boot. The
// alternative is a mount that looks like it works over an empty disk.
func TestFactoryRefusesARootThatIsNotADirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := localfs.Factory(map[string]string{"root": missing}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Factory(a missing root) = %v, want fs.ErrNotExist", err)
	}

	file := filepath.Join(t.TempDir(), "notes")
	if err := os.WriteFile(file, []byte("text"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}
	if _, err := localfs.Factory(map[string]string{"root": file}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(a root that is a file) = %v, want fs.ErrInvalid", err)
	}
}

// A setting localfs cannot honour is a misspelling of one meant for another
// kind, and boot is the last place an operator can still act on it.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	_, err := localfs.Factory(map[string]string{"root": t.TempDir(), "bucket": "photos"})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(bucket) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "bucket") {
		t.Errorf("Factory(bucket) = %v, want the setting named", err)
	}
}

// The registry is the only route into the tree, and it is what a config
// document reaches. A kind that cannot be registered and mounted is not a
// plugin, whatever it implements.
func TestLocalfsMountsFromAMountSpec(t *testing.T) {
	root := t.TempDir()
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registry.Register(localfs.Kind, localfs.Factory, localfs.RootSetting); err != nil {
		t.Fatalf("Register(localfs) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/disk", Kind: localfs.Kind, Settings: map[string]string{"root": root}}})
	if err != nil {
		t.Fatalf("Table(/disk) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/disk/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/disk/notes) = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "notes"))
	if err != nil {
		t.Fatalf("reading the host file = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("the host file = %q, want %q", content, "text")
	}
	if link := table.AsSymlinker(); link == nil {
		t.Error("AsSymlinker() through the table = nil, want the dispatcher")
	}
}
