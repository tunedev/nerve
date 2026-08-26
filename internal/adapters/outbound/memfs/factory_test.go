package memfs_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestFactoryBuildsAUsableTree(t *testing.T) {
	fsys, err := memfs.Factory(nil)
	if err != nil {
		t.Fatalf("Factory(nil) = %v", err)
	}

	ctx := context.Background()
	if err := fsys.Write(ctx, "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}
	content, err := fsys.Read(ctx, "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/notes) = %q, want %q", content, "text")
	}
}

// memfs needs nothing to be built, so a setting is a misspelling of one meant
// for another kind. Boot is where an operator can still do something about it.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	_, err := memfs.Factory(map[string]string{"root": "/tmp/nerve"})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(root) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("Factory(root) = %v, want the setting named", err)
	}
}

// Each mount is its own tree, so two memfs mounts in one table do not share
// entries.
func TestEachBuildIsItsOwnTree(t *testing.T) {
	ctx := context.Background()
	first, _ := memfs.Factory(nil)
	second, _ := memfs.Factory(nil)

	if err := first.Write(ctx, "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}

	if _, err := second.Read(ctx, "/notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/notes) on the second tree = %v, want fs.ErrNotExist", err)
	}
}

// The registry is the only route into the tree, and it is what a config
// document reaches. A kind that cannot be registered and mounted is not a
// plugin, whatever it implements.
func TestMemfsMountsFromAMountSpec(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registry.Register("memfs", memfs.Factory); err != nil {
		t.Fatalf("Register(memfs) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/scratch", Kind: "memfs"}})
	if err != nil {
		t.Fatalf("Table(/scratch) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/scratch/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/scratch/notes) = %v", err)
	}
	content, err := table.Read(ctx, "/scratch/notes")
	if err != nil {
		t.Fatalf("Read(/scratch/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/scratch/notes) = %q, want %q", content, "text")
	}

	if stream := table.AsStreamer(); stream == nil {
		t.Error("AsStreamer() through the table = nil, want the dispatcher")
	}
}
