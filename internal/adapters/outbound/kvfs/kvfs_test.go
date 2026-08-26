package kvfs_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The port contract is asserted once, in the conformance suite. What is left in
// this package is what only kvfs promises.
func mustWrite(t *testing.T, fsys *kvfs.FS, name, content string) {
	t.Helper()
	if err := fsys.Write(context.Background(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func names(entries []vfs.FileInfo) []string {
	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	return listed
}

// The namespace is the reason a store is shared rather than one per mount:
// session state and agent state are in one keyspace and never in each other's
// keys.
func TestNamespacesDoNotCollide(t *testing.T) {
	ctx := context.Background()
	store := kvfs.NewMemStore()
	sessions := kvfs.New(store, "sessions")
	agents := kvfs.New(store, "agents")

	mustWrite(t, sessions, "/abc/state", "running")
	mustWrite(t, agents, "/abc/state", "idle")

	if content, _ := sessions.Read(ctx, "/abc/state"); string(content) != "running" {
		t.Errorf("Read through the sessions mount = %q, want %q", content, "running")
	}
	if content, _ := agents.Read(ctx, "/abc/state"); string(content) != "idle" {
		t.Errorf("Read through the agents mount = %q, want %q", content, "idle")
	}
}

// One namespace is one set of keys whichever mount reaches it, which is what
// makes a mount a view of a keyspace rather than a container of its own.
func TestOneNamespaceIsOneKeyspace(t *testing.T) {
	store := kvfs.NewMemStore()
	mustWrite(t, kvfs.New(store, "sessions"), "/abc/state", "running")

	content, err := kvfs.New(store, "sessions").Read(context.Background(), "/abc/state")
	if err != nil {
		t.Fatalf("Read through a second mount of the same namespace = %v", err)
	}
	if string(content) != "running" {
		t.Errorf("Read = %q, want %q", content, "running")
	}
}

// Emptying a namespace leaves the ones beside it, or resetting one mount would
// take every other mount's keys with it.
func TestRemoveAllEmptiesOneNamespace(t *testing.T) {
	ctx := context.Background()
	store := kvfs.NewMemStore()
	sessions := kvfs.New(store, "sessions")
	agents := kvfs.New(store, "agents")
	mustWrite(t, sessions, "/abc/state", "running")
	mustWrite(t, agents, "/abc/state", "idle")

	if err := sessions.RemoveAll(ctx, "/"); err != nil {
		t.Fatalf("RemoveAll(/) = %v", err)
	}

	if _, err := sessions.Read(ctx, "/abc/state"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read after RemoveAll = %v, want fs.ErrNotExist", err)
	}
	if _, err := agents.Read(ctx, "/abc/state"); err != nil {
		t.Errorf("Read through the mount beside it = %v, want the value", err)
	}
}

// Listing a prefix is how a caller finds the sessions: the segment between two
// separators is a child of the prefix above it, and it carries the directory bit
// so a caller can walk to what is under it.
func TestReadDirListsPrefixesAsDirectories(t *testing.T) {
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")
	mustWrite(t, fsys, "/abc/state", "running")
	mustWrite(t, fsys, "/abc/history/first", "started")
	mustWrite(t, fsys, "/def/state", "idle")
	mustWrite(t, fsys, "/index", "two sessions")

	entries, err := fsys.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}

	want := []string{"abc", "def", "index"}
	if listed := names(entries); !slices.Equal(listed, want) {
		t.Fatalf("ReadDir(/) = %v, want %v", listed, want)
	}
	if !entries[0].IsDir() || entries[0].Size != 0 {
		t.Errorf("abc = %+v, want a directory of size 0", entries[0])
	}
	if entries[2].IsDir() || entries[2].Size != 12 {
		t.Errorf("index = %+v, want a file of size 12", entries[2])
	}
}

// A prefix is described by what is beneath it, so the latest write under one is
// what a caller sorting sessions by age sees.
func TestAPrefixCarriesTheLatestTimeBeneathIt(t *testing.T) {
	ctx := context.Background()
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")
	mustWrite(t, fsys, "/abc/history/first", "started")
	mustWrite(t, fsys, "/abc/state", "running")

	state, err := fsys.Stat(ctx, "/abc/state")
	if err != nil {
		t.Fatalf("Stat(/abc/state) = %v", err)
	}
	entries, err := fsys.ReadDir(ctx, "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}

	if len(entries) != 1 || !entries[0].ModTime.Equal(state.ModTime) {
		t.Errorf("ReadDir(/) = %+v, want one prefix stamped %v", entries, state.ModTime)
	}
}

// A key and a prefix can share a name, and a listing has to answer the same way
// whichever order the store hands its keys back in.
func TestAKeyBesideAPrefixOfTheSameName(t *testing.T) {
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")
	mustWrite(t, fsys, "/abc", "the session")
	mustWrite(t, fsys, "/abc/state", "running")

	for round := range 20 {
		entries, err := fsys.ReadDir(context.Background(), "/")
		if err != nil {
			t.Fatalf("ReadDir(/) = %v", err)
		}
		if len(entries) != 1 || entries[0].IsDir() || entries[0].Size != 11 {
			t.Fatalf("ReadDir(/) on round %d = %+v, want the key alone", round, entries)
		}
	}
}

// A prefix exists only because keys are under it, so there is nothing at one to
// describe — the root included, which is the prefix every key is under.
func TestStatAnswersForKeysAndNotPrefixes(t *testing.T) {
	ctx := context.Background()
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")
	mustWrite(t, fsys, "/abc/state", "running")

	if _, err := fsys.Stat(ctx, "/abc"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of a prefix with keys under it = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Stat(ctx, "/"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Stat(/) = %v, want fs.ErrInvalid", err)
	}
}

// A path arrives however the caller spelled it. The spellings are joined by
// hand: path.Join would clean them first, which is the work being asserted.
func TestPathsResolveToOneKey(t *testing.T) {
	fsys := kvfs.New(kvfs.NewMemStore(), "sessions")
	mustWrite(t, fsys, "/abc/state", "running")

	for _, name := range []string{"//abc/state", "/abc/./state", "/abc/history/../state", "abc/state"} {
		if _, err := fsys.Stat(context.Background(), name); err != nil {
			t.Errorf("Stat(%q) = %v, want the value at /abc/state", name, err)
		}
	}
}
