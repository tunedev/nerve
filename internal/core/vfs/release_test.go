package vfs_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// holder is a plugin that holds something to release, which is the only thing
// separating it from every other fake here. It writes its name to a log the
// case owns, so a case can see both that it was released and when.
type holder struct {
	*recorder
	log      *[]string
	refusing error
	releases int
}

func (h *holder) Close() error {
	h.releases++
	*h.log = append(*h.log, h.name)
	return h.refusing
}

// held returns a plugin that writes its release to log.
func held(name string, log *[]string) *holder {
	return &holder{recorder: &recorder{name: name}, log: log}
}

// holding registers one kind per plugin, named after it, so a case names the
// kind it wants mounted and keeps the plugin to look at afterwards.
func holding(t *testing.T, plugins ...*holder) *vfs.Registry {
	t.Helper()

	registry := vfs.NewRegistry(time.Minute, vfs.Retry{}, vfs.Breaker{})
	for _, plugin := range plugins {
		factory := func(map[string]string) (vfs.FileSystem, error) { return plugin, nil }
		if err := registry.Register(plugin.name, factory); err != nil {
			t.Fatalf("register %q: %v", plugin.name, err)
		}
	}
	return registry
}

// The ticket in one test: a mount built from config holds something an
// operating system knows about, and nothing that comes back out of the table
// can give it up. The registry saw the bare plugin, so the registry is what
// hands it back.
func TestReleaseClosesWhatAMountedPluginHolds(t *testing.T) {
	var log []string
	registry := holding(t, held("journal", &log))

	if _, err := registry.Table([]vfs.MountSpec{{Prefix: "/journal", Kind: "journal"}}); err != nil {
		t.Fatalf("Table = %v", err)
	}
	if err := registry.Release(); err != nil {
		t.Fatalf("Release = %v", err)
	}

	if want := []string{"journal"}; !slices.Equal(log, want) {
		t.Errorf("released %v, want %v", log, want)
	}
}

// A kind that composes another builds its target with Plugin, so the target is
// built first and the mount over it second. Releasing in the order they were
// built would take a target away from something still holding it.
func TestReleaseClosesInTheReverseOfTheOrderItBuilt(t *testing.T) {
	var log []string
	target, composed := held("target", &log), held("composed", &log)

	registry := holding(t, target)
	build := func(map[string]string) (vfs.FileSystem, error) {
		if _, err := registry.Plugin(vfs.MountSpec{Kind: "target"}); err != nil {
			return nil, err
		}
		return composed, nil
	}
	if err := registry.Register("composed", build); err != nil {
		t.Fatalf("register composed: %v", err)
	}

	if _, err := registry.Table([]vfs.MountSpec{{Prefix: "/proxy", Kind: "composed"}}); err != nil {
		t.Fatalf("Table = %v", err)
	}
	if err := registry.Release(); err != nil {
		t.Fatalf("Release = %v", err)
	}

	if want := []string{"composed", "target"}; !slices.Equal(log, want) {
		t.Errorf("released %v, want %v", log, want)
	}
}

// One mount that will not let go is not a reason to leave the others held, and
// not a reason to say nothing about it either.
func TestReleaseReportsARefusalAndReleasesTheRest(t *testing.T) {
	var log []string
	refused := errors.New("the pool is still in use")
	stuck := held("stuck", &log)
	stuck.refusing = refused
	registry := holding(t, stuck, held("willing", &log))

	if _, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/stuck", Kind: "stuck"},
		{Prefix: "/willing", Kind: "willing"},
	}); err != nil {
		t.Fatalf("Table = %v", err)
	}

	err := registry.Release()

	if !errors.Is(err, refused) {
		t.Errorf("Release = %v, want the refusal", err)
	}
	if want := []string{"willing", "stuck"}; !slices.Equal(log, want) {
		t.Errorf("released %v, want %v", log, want)
	}
}

// What has been released is no longer held, so a drain that runs twice does not
// close one database twice.
func TestReleaseHoldsNothingAfterwards(t *testing.T) {
	var log []string
	plugin := held("journal", &log)
	registry := holding(t, plugin)

	if _, err := registry.Table([]vfs.MountSpec{{Prefix: "/journal", Kind: "journal"}}); err != nil {
		t.Fatalf("Table = %v", err)
	}
	for range 2 {
		if err := registry.Release(); err != nil {
			t.Fatalf("Release = %v", err)
		}
	}

	if plugin.releases != 1 {
		t.Errorf("released %d times, want 1", plugin.releases)
	}
}

// Most kinds hold nothing to give up, and draining a table of them is not an
// error and not a special case.
func TestReleaseIsNothingToDoForAPluginThatHoldsNothing(t *testing.T) {
	registry := registered(t, "recording", recording)

	if _, err := registry.Table([]vfs.MountSpec{{Prefix: "/", Kind: "recording"}}); err != nil {
		t.Fatalf("Table = %v", err)
	}
	if err := registry.Release(); err != nil {
		t.Errorf("Release = %v, want nil", err)
	}
}
