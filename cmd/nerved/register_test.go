package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
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

// sqlfs is registered here rather than mounted, because building one opens a
// database this test would have no way to close: the plugin comes back out of
// the registry wrapped (nrv-mii). What the composition root is answerable for
// is that the kind a config can name is there, and that the setting it declares
// is the one an operator is allowed to read back.
func TestRegisterKindsDeclaresSqlfsAndItsDatabaseFile(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	if public := registry.Public(sqlfs.Kind); !slices.Equal(public, []string{sqlfs.PathSetting}) {
		t.Errorf("Public(sqlfs) = %v, want [%s]", public, sqlfs.PathSetting)
	}

	_, err := registry.Table([]vfs.MountSpec{{Prefix: "/scratch", Kind: sqlfs.Kind}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Table(sqlfs with no database file) = %v, want fs.ErrInvalid", err)
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

// A kvfs mount is a namespaced part of the one keyspace the composition root
// owns, so two mounts the config names do not answer each other's keys.
func TestRegisterKindsMakesKvfsMountable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/sessions", Kind: "kvfs", Settings: map[string]string{"namespace": "sessions"}},
		{Prefix: "/agents", Kind: "kvfs", Settings: map[string]string{"namespace": "agents"}},
	})
	if err != nil {
		t.Fatalf("Table(/sessions, /agents) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/sessions/abc/state", []byte("running")); err != nil {
		t.Fatalf("Write(/sessions/abc/state) = %v", err)
	}
	content, err := table.Read(ctx, "/sessions/abc/state")
	if err != nil {
		t.Fatalf("Read(/sessions/abc/state) = %v", err)
	}
	if string(content) != "running" {
		t.Errorf("Read(/sessions/abc/state) = %q, want %q", content, "running")
	}

	if _, err := table.Read(ctx, "/agents/abc/state"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/agents/abc/state) = %v, want fs.ErrNotExist", err)
	}
}

// The namespace is what an operator reads back to know which keys a mount is
// over, so it is declared public where a credential would not be.
func TestKvfsDeclaresItsNamespacePublic(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	if public := registry.Public("kvfs"); !slices.Equal(public, []string{"namespace"}) {
		t.Errorf("Public(kvfs) = %v, want [namespace]", public)
	}
}

// objectfs reaches a store an operator names, and the two settings that reach
// it are credentials. Declaring only the other three is what makes them
// redacted everywhere the effective config is read back.
func TestObjectfsDeclaresOnlyWhatIsNotACredential(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	want := []string{"endpoint", "bucket", "region"}
	if public := registry.Public("objectfs"); !slices.Equal(public, want) {
		t.Errorf("Public(objectfs) = %v, want %v", public, want)
	}
	for _, credential := range []string{"access_key_id", "secret_access_key"} {
		if slices.Contains(registry.Public("objectfs"), credential) {
			t.Errorf("Public(objectfs) names %q, want it redacted", credential)
		}
	}
}

// A mount of a kind the build has is built at boot, so a misspelled setting or
// a missing credential is a boot failure an operator can still act on.
func TestObjectfsMountRefusesSettingsItCannotHonour(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	spec := vfs.MountSpec{Prefix: "/objects", Kind: "objectfs", Settings: map[string]string{"bucket": "nerve"}}
	if _, err := registry.Table([]vfs.MountSpec{spec}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Table with an unreachable objectfs mount = %v, want fs.ErrInvalid", err)
	}
}
