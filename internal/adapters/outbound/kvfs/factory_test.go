package kvfs_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestFactoryBuildsANamespacedMount(t *testing.T) {
	build := kvfs.Factory(kvfs.NewMemStore())

	fsys, err := build(map[string]string{kvfs.NamespaceSetting: "sessions"})
	if err != nil {
		t.Fatalf("Factory(namespace=sessions) = %v", err)
	}

	ctx := context.Background()
	if err := fsys.Write(ctx, "/abc/state", []byte("running")); err != nil {
		t.Fatalf("Write(/abc/state) = %v", err)
	}
	content, err := fsys.Read(ctx, "/abc/state")
	if err != nil {
		t.Fatalf("Read(/abc/state) = %v", err)
	}
	if string(content) != "running" {
		t.Errorf("Read(/abc/state) = %q, want %q", content, "running")
	}
}

// Every mount from one factory is over one keyspace, so a mount that does not
// name its part of it would answer another mount's keys. Boot is where an
// operator can still do something about that.
func TestFactoryRefusesAMountWithNoNamespace(t *testing.T) {
	build := kvfs.Factory(kvfs.NewMemStore())

	for _, settings := range []map[string]string{nil, {kvfs.NamespaceSetting: ""}, {kvfs.NamespaceSetting: "/"}} {
		_, err := build(settings)
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(%v) = %v, want fs.ErrInvalid", settings, err)
		}
		if !strings.Contains(err.Error(), kvfs.NamespaceSetting) {
			t.Errorf("Factory(%v) = %v, want the setting named", settings, err)
		}
	}
}

// A setting kvfs cannot honour is a misspelling of one meant for another kind.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	build := kvfs.Factory(kvfs.NewMemStore())

	_, err := build(map[string]string{kvfs.NamespaceSetting: "sessions", "root": "/tmp/nerve"})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(root) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("Factory(root) = %v, want the setting named", err)
	}
}

// The registry is the only route into the tree, and it is what a config
// document reaches. Two mounts of one kind over one store is the arrangement
// the namespace exists for, so it is the one asserted here.
func TestKvfsMountsFromMountSpecs(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registry.Register(kvfs.Kind, kvfs.Factory(kvfs.NewMemStore()), kvfs.NamespaceSetting); err != nil {
		t.Fatalf("Register(kvfs) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/sessions", Kind: kvfs.Kind, Settings: map[string]string{kvfs.NamespaceSetting: "sessions"}},
		{Prefix: "/agents", Kind: kvfs.Kind, Settings: map[string]string{kvfs.NamespaceSetting: "agents"}},
	})
	if err != nil {
		t.Fatalf("Table(/sessions, /agents) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/sessions/abc/state", []byte("running")); err != nil {
		t.Fatalf("Write(/sessions/abc/state) = %v", err)
	}

	if _, err := table.Read(ctx, "/agents/abc/state"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/agents/abc/state) = %v, want fs.ErrNotExist", err)
	}
	if caps := table.Capabilities("/sessions/abc/state"); !caps.ObjectStore {
		t.Errorf("Capabilities through the table = %+v, want ObjectStore forwarded", caps)
	}
	if stream := table.AsStreamer(); stream != nil {
		t.Errorf("AsStreamer() through the table = %v, want nil", stream)
	}
}
