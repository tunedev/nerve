package kvfs_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
)

// stores are the implementations of the port, run against one set of cases.
// That is the whole point of the port having two: what kvfs is written against
// is a promise, and a promise only one implementation keeps is a struct with an
// interface in front of it.
func stores() map[string]func(*testing.T) kvfs.Store {
	return map[string]func(*testing.T) kvfs.Store{
		"MemStore":    func(*testing.T) kvfs.Store { return kvfs.NewMemStore() },
		"ObjectStore": func(*testing.T) kvfs.Store { return kvfs.NewObjectStore(newMemBucket()) },
	}
}

// storeCase is one promise of the port under the name it is reported by.
type storeCase struct {
	name string
	run  func(t *testing.T, store kvfs.Store)
}

// storeCases are the promises of the port. They are named here rather than
// inline so the run against a real object store is the same set, not a second
// set that drifts from this one.
func storeCases() []storeCase {
	return []storeCase{
		{"Head describes a key without its content", headDescribes},
		{"Head of an absent key does not exist", headOfAnAbsentKey},
		{"Get of an absent key does not exist", getOfAnAbsentKey},
		{"Put replaces what was there", putReplaces},
		{"Put copies the caller's slice", putCopies},
		{"Get hands back a copy", getCopies},
		{"Put moves the instant forward", putMovesTheInstantForward},
		{"Delete of an absent key does not exist", deleteOfAnAbsentKey},
		{"Move carries the value across", moveCarriesTheValue},
		{"Move replaces what it lands on", moveReplacesTheLanding},
		{"Move of an absent key does not exist", moveOfAnAbsentKey},
		{"List answers what is under a prefix", listAnswersAPrefix},
		{"List of a prefix nothing is under is empty", listOfAnUnusedPrefix},
	}
}

func TestStorePort(t *testing.T) {
	for name, build := range stores() {
		t.Run(name, func(t *testing.T) { runStoreCases(t, build) })
	}
}

// runStoreCases gives every case a keyspace of its own, because each one starts
// from a store with nothing in it. The case's own *testing.T reaches build, so
// a store that has to scope itself by case name can.
func runStoreCases(t *testing.T, build func(*testing.T) kvfs.Store) {
	t.Helper()
	for _, one := range storeCases() {
		t.Run(one.name, func(t *testing.T) { one.run(t, build(t)) })
	}
}

func mustPut(t *testing.T, store kvfs.Store, key, content string, mode fs.FileMode) {
	t.Helper()
	if err := store.Put(context.Background(), key, []byte(content), mode); err != nil {
		t.Fatalf("Put(%q) = %v", key, err)
	}
}

func mustGet(t *testing.T, store kvfs.Store, key string) kvfs.Value {
	t.Helper()
	value, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q) = %v", key, err)
	}
	return value
}

func headDescribes(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o600)

	one, err := store.Head(t.Context(), "sessions/abc")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	if one.Key != "sessions/abc" || one.Size != int64(len("running")) || one.Mode.Perm() != 0o600 {
		t.Errorf("Head = %+v, want the key at size %d in mode 0600", one, len("running"))
	}
	if one.ModTime.IsZero() {
		t.Error("Head reported no time, want the instant the store wrote it")
	}
}

func headOfAnAbsentKey(t *testing.T, store kvfs.Store) {
	if _, err := store.Head(t.Context(), "sessions/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Head of an absent key = %v, want fs.ErrNotExist", err)
	}
}

func getOfAnAbsentKey(t *testing.T, store kvfs.Store) {
	if _, err := store.Get(t.Context(), "sessions/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get of an absent key = %v, want fs.ErrNotExist", err)
	}
}

func putReplaces(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o644)
	mustPut(t, store, "sessions/abc", "idle", 0o600)

	value := mustGet(t, store, "sessions/abc")
	if string(value.Data) != "idle" || value.Mode.Perm() != 0o600 {
		t.Errorf("Get after a second Put = %q in mode %v, want %q in mode 0600", value.Data, value.Mode.Perm(), "idle")
	}
}

// What the caller holds after a Put is not what the store holds, or a caller
// reusing a buffer would change a value it had already written.
func putCopies(t *testing.T, store kvfs.Store) {
	data := []byte("running")
	if err := store.Put(t.Context(), "sessions/abc", data, 0o644); err != nil {
		t.Fatalf("Put = %v", err)
	}
	copy(data, "STOPPED")

	if value := mustGet(t, store, "sessions/abc"); string(value.Data) != "running" {
		t.Errorf("Get after the caller changed its slice = %q, want %q", value.Data, "running")
	}
}

// What Get hands back is not what the store holds either, so writing through
// the returned slice cannot reach the stored value.
func getCopies(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o644)

	first := mustGet(t, store, "sessions/abc")
	copy(first.Data, "STOPPED")

	if second := mustGet(t, store, "sessions/abc"); string(second.Data) != "running" {
		t.Errorf("Get after writing through an earlier one = %q, want %q", second.Data, "running")
	}
}

// A store stamps the write. Its clock may advance in ticks rather than
// continuously, so the case writes until the instant has moved rather than
// weakening what it asserts: a store that never stamps never gets there.
func putMovesTheInstantForward(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o644)
	before := mustGet(t, store, "sessions/abc").ModTime

	deadline := time.Now().Add(2 * time.Second)
	for {
		mustPut(t, store, "sessions/abc", "idle", 0o644)
		if mustGet(t, store, "sessions/abc").ModTime.After(before) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the instant after a second Put is still %v, want it later", before)
		}
		time.Sleep(time.Millisecond)
	}
}

// A bucket answers a delete of nothing with success. The port does not, because
// the plugin above turns it into Remove of a path, and a caller that removed a
// session that was not there is told so.
func deleteOfAnAbsentKey(t *testing.T, store kvfs.Store) {
	if err := store.Delete(t.Context(), "sessions/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Delete of an absent key = %v, want fs.ErrNotExist", err)
	}
}

// A move is not a write: the content, the mode and the instant all arrive at
// the new key unchanged, and nothing is left at the old one.
func moveCarriesTheValue(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o600)
	before := mustGet(t, store, "sessions/abc")

	if err := store.Move(t.Context(), "sessions/abc", "sessions/def"); err != nil {
		t.Fatalf("Move = %v", err)
	}

	after := mustGet(t, store, "sessions/def")
	if !bytes.Equal(after.Data, before.Data) || after.Mode.Perm() != 0o600 || !after.ModTime.Equal(before.ModTime) {
		t.Errorf("Get after a Move = %q in mode %v at %v, want %q in mode 0600 at %v",
			after.Data, after.Mode.Perm(), after.ModTime, before.Data, before.ModTime)
	}
	if _, err := store.Get(t.Context(), "sessions/abc"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get of the key that moved = %v, want fs.ErrNotExist", err)
	}
}

func moveReplacesTheLanding(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o644)
	mustPut(t, store, "sessions/def", "idle", 0o644)

	if err := store.Move(t.Context(), "sessions/abc", "sessions/def"); err != nil {
		t.Fatalf("Move = %v", err)
	}

	if value := mustGet(t, store, "sessions/def"); string(value.Data) != "running" {
		t.Errorf("Get of the key moved onto = %q, want %q", value.Data, "running")
	}
}

func moveOfAnAbsentKey(t *testing.T, store kvfs.Store) {
	if err := store.Move(t.Context(), "sessions/absent", "sessions/def"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Move of an absent key = %v, want fs.ErrNotExist", err)
	}
}

// A listing is every key under the prefix however deep, and nothing outside it:
// kvfs folds the segments into children and needs the keys below the first
// separator to know a prefix is there at all.
//
// The two keys under the prefix do not share a name, and that is deliberate. A
// key beside a prefix of the same name is a listing question the plugin above
// answers and the conformance suite asks, and MinIO hands a listing only one
// half of such a pair — see docs/notes/e2.8-object-backed-kv.md. Asking it here
// would hold every store to something one real store does not do.
func listAnswersAPrefix(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o644)
	mustPut(t, store, "sessions/def/state", "started", 0o644)
	mustPut(t, store, "agents/one", "idle", 0o644)

	found, err := store.List(t.Context(), "sessions/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}

	listed := make([]string, len(found))
	for i, one := range found {
		listed[i] = one.Key
	}
	slices.Sort(listed)

	want := []string{"sessions/abc", "sessions/def/state"}
	if !slices.Equal(listed, want) {
		t.Errorf("List(%q) = %v, want %v", "sessions/", listed, want)
	}
}

func listOfAnUnusedPrefix(t *testing.T, store kvfs.Store) {
	mustPut(t, store, "sessions/abc", "running", 0o644)

	found, err := store.List(t.Context(), "agents/")
	if err != nil {
		t.Fatalf("List of a prefix nothing is under = %v, want no error", err)
	}
	if len(found) != 0 {
		t.Errorf("List of a prefix nothing is under = %v, want nothing", found)
	}
}
