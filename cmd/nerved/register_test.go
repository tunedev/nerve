package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// The composition root is the only place that knows a concrete plugin, so it is
// the only place a kind can be registered — and a kind the config can name is
// the whole point of the registry.
func TestRegisterKindsMakesMemfsMountable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
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
}

// memfs takes no settings, so it declares none safe to read back and the
// effective-config surface has nothing of its to show.
func TestMemfsDeclaresNoPublicSettings(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	if public := registry.Public("memfs"); len(public) != 0 {
		t.Errorf("Public(memfs) = %v, want none", public)
	}
}

// localfs is the second kind, and the point of a registry is that a second one
// is a line here rather than an edit to a switch.
func TestRegisterKindsMakesLocalfsMountable(t *testing.T) {
	root := t.TempDir()
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/disk", Kind: "localfs", Settings: map[string]string{"root": root}}})
	if err != nil {
		t.Fatalf("Table(/disk) = %v", err)
	}

	if err := table.Write(context.Background(), "/disk/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/disk/notes) = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes"))
	if err != nil {
		t.Fatalf("reading the host file = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("the host file = %q, want %q", content, "text")
	}
}

// A host directory is not a credential, and an operator reading the effective
// config has to be able to see which disk a mount is on.
func TestLocalfsDeclaresItsRootPublic(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	if public := registry.Public("localfs"); !slices.Equal(public, []string{"root"}) {
		t.Errorf("Public(localfs) = %v, want [root]", public)
	}
}
