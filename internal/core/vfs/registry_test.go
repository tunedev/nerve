package vfs_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// recording builds a recorder named by its settings, standing in for a real
// storage kind: the whole point is that the registry never knows what it is.
func recording(settings map[string]string) (vfs.FileSystem, error) {
	return &recorder{name: settings["name"]}, nil
}

// registered returns a registry holding one kind under name.
func registered(t *testing.T, name string, factory vfs.Factory) *vfs.Registry {
	t.Helper()

	registry := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{})
	if err := registry.Register(name, factory); err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
	return registry
}

// The whole ticket in one test: two mounts that exist only because config named
// them, resolving to different plugins, with nothing in this file switching on
// a kind.
func TestConfigAloneAddsAMount(t *testing.T) {
	registry := registered(t, "recording", recording)

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/", Kind: "recording", Settings: map[string]string{"name": "root"}},
		{Prefix: "/queue", Kind: "recording", Settings: map[string]string{"name": "queue"}},
	})
	if err != nil {
		t.Fatalf("build table: %v", err)
	}

	for path, want := range map[string]string{"/thing": "root", "/queue/jobs/next": "queue"} {
		got, err := table.Read(context.Background(), path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("read %q reached mount %q, want %q", path, got, want)
		}
	}
}

func TestSettingsReachTheFactory(t *testing.T) {
	var got map[string]string
	registry := registered(t, "recording", func(settings map[string]string) (vfs.FileSystem, error) {
		got = settings
		return &recorder{}, nil
	})

	want := map[string]string{"root": "/var/lib/nerve", "mode": "0700"}
	if _, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "recording", Settings: want}}); err != nil {
		t.Fatalf("build table: %v", err)
	}

	for key, value := range want {
		if got[key] != value {
			t.Errorf("factory saw %s=%q, want %q", key, got[key], value)
		}
	}
}

func TestUnknownKindIsRefusedAndNamesWhatIsRegistered(t *testing.T) {
	registry := registered(t, "recording", recording)

	_, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "recordng"}})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unknown kind gave %v, want fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "recording") {
		t.Errorf("error %q does not name the registered kind", err)
	}
}

func TestAKindIsRegisteredOnce(t *testing.T) {
	registry := registered(t, "recording", recording)

	if err := registry.Register("recording", recording); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second registration gave %v, want fs.ErrExist", err)
	}
}

func TestAFactoryFailureNamesTheMountItCameFrom(t *testing.T) {
	broken := errors.New("no such directory")
	registry := registered(t, "recording", func(map[string]string) (vfs.FileSystem, error) {
		return nil, broken
	})

	_, err := registry.Table([]vfs.MountSpec{{Prefix: "/queue", Kind: "recording"}})
	if !errors.Is(err, broken) {
		t.Fatalf("build gave %v, want the factory error", err)
	}
	if !strings.Contains(err.Error(), "/queue") {
		t.Errorf("error %q does not name the mount", err)
	}
}

func TestATableRefusesTwoMountsAtOnePrefix(t *testing.T) {
	registry := registered(t, "recording", recording)

	_, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/queue", Kind: "recording"},
		{Prefix: "/queue", Kind: "recording"},
	})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("duplicate prefix gave %v, want fs.ErrExist", err)
	}
}

func TestConfigNamingNoMountsMountsNothing(t *testing.T) {
	table, err := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{}).Table(nil)
	if err != nil {
		t.Fatalf("build table: %v", err)
	}

	if _, err := table.Read(context.Background(), "/thing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read on an empty table gave %v, want fs.ErrNotExist", err)
	}
}

// A registry with nothing in it still says something an operator can act on.
func TestAnEmptyRegistryTellsTheOperatorItIsEmpty(t *testing.T) {
	_, err := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{}).Table([]vfs.MountSpec{{Prefix: "/", Kind: "disk"}})
	if err == nil {
		t.Fatal("building an unregistered kind succeeded")
	}
	if !strings.Contains(err.Error(), "this build has no kinds") {
		t.Errorf("error %q does not say the registry is empty", err)
	}
}

// A kind says which of its settings can be read back. Nothing else can tell a
// bucket name from an access key, so what a kind does not name is not echoed.
func TestRegistryReportsTheSettingsAKindDeclares(t *testing.T) {
	registry := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{})
	if err := registry.Register("s3", recording, "endpoint", "bucket"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register("silent", recording); err != nil {
		t.Fatalf("register: %v", err)
	}

	if got := registry.Public("s3"); !slices.Equal(got, []string{"endpoint", "bucket"}) {
		t.Errorf("Public(s3) = %v, want the declared keys", got)
	}
	if got := registry.Public("silent"); len(got) != 0 {
		t.Errorf("Public(silent) = %v, want none — a kind that declares nothing echoes nothing", got)
	}
	if got := registry.Public("unregistered"); len(got) != 0 {
		t.Errorf("Public(unregistered) = %v, want none", got)
	}
}
