package streamrotatefs_test

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamrotatefs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestFactoryHonoursTheSizeAMountNames(t *testing.T) {
	fsys, err := streamrotatefs.Factory(map[string]string{"size": "4"})
	if err != nil {
		t.Fatalf("Factory(size=4) = %v", err)
	}

	mustWrite(t, fsys, "/log", "abcdef")

	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"log", "log.1"}) {
		t.Errorf("ReadDir after a write past the size = %v, want [log log.1]", listed)
	}
}

func TestFactoryHonoursTheKeepAMountNames(t *testing.T) {
	fsys, err := streamrotatefs.Factory(map[string]string{"size": "4", "keep": "1"})
	if err != nil {
		t.Fatalf("Factory(size=4 keep=1) = %v", err)
	}

	mustWrite(t, fsys, "/log", "aaaa")
	mustWrite(t, fsys, "/log", "bbbb")

	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"log", "log.2"}) {
		t.Errorf("ReadDir of a mount keeping one = %v, want [log log.2]", listed)
	}
}

// A segment sealed before anything is in it is a log of empty segments, so a
// mount that asks for one is refused at boot rather than left to fill a mount
// with nothing.
func TestFactoryRefusesASizeThatHoldsNothing(t *testing.T) {
	for _, spelled := range []string{"0", "-1", "plenty"} {
		_, err := streamrotatefs.Factory(map[string]string{"size": spelled})

		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(size=%s) = %v, want fs.ErrInvalid", spelled, err)
		}
	}
}

// Keeping none is a mount that rotates and discards, which is asked for. Fewer
// than none is not a number of segments.
func TestFactoryRefusesAKeepThatIsNotACount(t *testing.T) {
	if _, err := streamrotatefs.Factory(map[string]string{"keep": "0"}); err != nil {
		t.Errorf("Factory(keep=0) = %v, want a mount that rotates and discards", err)
	}
	for _, spelled := range []string{"-1", "a few"} {
		_, err := streamrotatefs.Factory(map[string]string{"keep": spelled})

		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(keep=%s) = %v, want fs.ErrInvalid", spelled, err)
		}
	}
}

// A setting this kind cannot honour is a misspelling of one meant for another,
// and boot is the last place an operator can still act on it.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	_, err := streamrotatefs.Factory(map[string]string{"capacity": "4096"})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(capacity) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "capacity") {
		t.Errorf("Factory(capacity) = %v, want the setting named", err)
	}
}

// A mount that names nothing rotates at the default size and keeps the default
// number of segments, which is what makes the kind mountable with no settings
// at all.
func TestFactoryDefaultsToASizeAndAKeep(t *testing.T) {
	fsys, err := streamrotatefs.Factory(nil)
	if err != nil {
		t.Fatalf("Factory(nothing) = %v", err)
	}

	mustWrite(t, fsys, "/log", "short")

	if listed := mustList(t, fsys, "/"); !slices.Equal(listed, []string{"log"}) {
		t.Errorf("ReadDir after a write far inside the default size = %v, want [log]", listed)
	}
}

// The registry is the only route into a mount, so rotation has to be visible
// through the dispatcher and not only through the plugin.
func TestStreamrotatefsMountsFromAMountSpec(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registry.Register(streamrotatefs.Kind, streamrotatefs.Factory, streamrotatefs.Public...); err != nil {
		t.Fatalf("Register(streamrotatefs) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/logs", Kind: streamrotatefs.Kind, Settings: map[string]string{"size": "4"}},
	})
	if err != nil {
		t.Fatalf("Table(/logs) = %v", err)
	}

	mustWrite(t, table, "/logs/app", "aaaa")

	if listed := mustList(t, table, "/logs"); !slices.Equal(listed, []string{"app", "app.1"}) {
		t.Errorf("ReadDir(/logs) through the table = %v, want [app app.1]", listed)
	}
	if content := mustRead(t, table, "/logs/app.1"); content != "aaaa" {
		t.Errorf("Read(/logs/app.1) through the table = %q, want %q", content, "aaaa")
	}
}
