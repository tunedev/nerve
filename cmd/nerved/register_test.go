package main

import (
	"context"
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
