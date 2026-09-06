package graphfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
)

// The acceptance of the ticket. A graph is snapshotted, changed past
// recognition, and set back to what it was — over a store a second process
// opened, so what the restore reads is the record rather than anything the
// first graph still held.
func TestASnapshotTakenBeforeARestartIsWhatARestoreGivesBack(t *testing.T) {
	store := newMemStore()
	graph := newGraph(t, store, statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/snapshots/before", "about to reindex")

	write(t, graph, "/docs/stack", "Nerve uses Neo4j")
	write(t, graph, "/docs/crew", "Furiosa drives Rig")

	// The extractor refuses, so nothing this graph answers can have come from a
	// second reading of a text.
	again, err := graphfs.Open(t.Context(), "notes", edges, store, store, statements{refuse: errors.New("no endpoint")})
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if answer := read(t, again, "/neighbours/Nerve"); !strings.Contains(answer, "Neo4j") {
		t.Fatalf("Read(/neighbours/Nerve) = %q, want what the graph was left holding", answer)
	}

	write(t, again, "/restore", "before")

	if answer := read(t, again, "/neighbours/Nerve"); !strings.Contains(answer, "Postgres") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want the claim the snapshot held", answer)
	}
	if answer := read(t, again, "/neighbours/Nerve"); strings.Contains(answer, "Neo4j") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want nothing written after the snapshot", answer)
	}
	if body := read(t, again, "/docs/stack"); body != "Nerve uses Postgres" {
		t.Errorf("Read(/docs/stack) = %q, want the text as the snapshot held it", body)
	}
	if _, err := again.Read(t.Context(), "/docs/crew"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/docs/crew) = %v, want a text the snapshot never had to be gone", err)
	}
}

// A restore is a change to the record like any other, so the graph it left is
// the graph the next process opens.
func TestARestoredGraphIsWhatOpensAgain(t *testing.T) {
	store := newMemStore()
	graph := newGraph(t, store, statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/snapshots/before", "")
	write(t, graph, "/docs/stack", "Nerve uses Neo4j")
	write(t, graph, "/restore", "before")

	again, err := graphfs.Open(t.Context(), "notes", edges, store, store, statements{refuse: errors.New("no endpoint")})
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if body := read(t, again, "/docs/stack"); body != "Nerve uses Postgres" {
		t.Errorf("Read(/docs/stack) = %q, want the restored text", body)
	}
	if standing := read(t, again, "/restore"); !strings.Contains(standing, "standing before") {
		t.Errorf("Read(/restore) = %q, want the label the graph was set back to", standing)
	}
}

// The restore path names where the graph stands, and only while it stands
// there: anything written after a restore is a graph that is no longer the
// snapshot, and the path says so by holding nothing.
func TestTheRestorePathHoldsALabelOnlyWhileNothingHasBeenWrittenSince(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})

	if standing := read(t, graph, "/restore"); standing != "" {
		t.Errorf("Read(/restore) = %q, want nothing from a graph that was never restored", standing)
	}

	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/snapshots/before", "")
	write(t, graph, "/restore", "before")

	if standing := read(t, graph, "/restore"); !strings.Contains(standing, "standing before") {
		t.Fatalf("Read(/restore) = %q, want the label just restored", standing)
	}

	write(t, graph, "/docs/crew", "Furiosa drives Rig")

	if standing := read(t, graph, "/restore"); standing != "" {
		t.Errorf("Read(/restore) = %q, want nothing once the graph has been written to", standing)
	}
}

// A snapshot describes itself: when it was taken, how much it kept, and the
// note written with it. What it does not hand back is the blob — the way back
// is the restore path.
func TestASnapshotDescribesItself(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/docs/crew", "Furiosa drives Rig")
	write(t, graph, "/snapshots/before", "about to reindex")

	described := read(t, graph, "/snapshots/before")
	for _, want := range []string{"taken ", "texts 2", "note about to reindex"} {
		if !strings.Contains(described, want) {
			t.Errorf("Read(/snapshots/before) = %q, want %q in it", described, want)
		}
	}

	info, err := graph.Stat(t.Context(), "/snapshots/before")
	if err != nil {
		t.Fatalf("Stat = %v", err)
	}
	if info.Size != int64(len(described)) || info.ModTime.IsZero() {
		t.Errorf("Stat(/snapshots/before) = %+v, want the description's size and the instant it was taken", info)
	}
}

// The snapshots list, unlike the expansions: a label exists because somebody
// took it, so there is a set to read before deciding which to go back to.
func TestTheSnapshotsList(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	for _, label := range []string{"friday", "before", "monday"} {
		write(t, graph, "/snapshots/"+label, "")
	}

	entries, err := graph.ReadDir(t.Context(), "/snapshots")
	if err != nil {
		t.Fatalf("ReadDir = %v", err)
	}

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if strings.Join(names, " ") != "before friday monday" {
		t.Errorf("ReadDir(/snapshots) = %v, want every label, sorted", names)
	}
}

// A snapshot is forgotten by removing it, and the graph is not touched: a
// snapshot is what the graph was, and forgetting it says nothing about what the
// graph is.
func TestASnapshotIsForgottenByRemovingIt(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/snapshots/before", "")

	if err := graph.Remove(t.Context(), "/snapshots/before"); err != nil {
		t.Fatalf("Remove = %v", err)
	}
	if _, err := graph.Read(t.Context(), "/snapshots/before"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/snapshots/before) = %v, want fs.ErrNotExist", err)
	}
	if body := read(t, graph, "/docs/stack"); body != "Nerve uses Postgres" {
		t.Errorf("Read(/docs/stack) = %q, want the graph untouched by the removal", body)
	}
	if err := graph.Remove(t.Context(), "/snapshots/before"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove of a label already gone = %v, want fs.ErrNotExist", err)
	}
}

// RemoveAll under the snapshots forgets one label or every one of them, and is
// idempotent either way.
func TestRemoveAllForgetsEverySnapshot(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/snapshots/before", "")
	write(t, graph, "/snapshots/after", "")

	if err := graph.RemoveAll(t.Context(), "/snapshots"); err != nil {
		t.Fatalf("RemoveAll = %v", err)
	}
	entries, err := graph.ReadDir(t.Context(), "/snapshots")
	if err != nil {
		t.Fatalf("ReadDir = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/snapshots) = %v, want nothing left", entries)
	}
	if err := graph.RemoveAll(t.Context(), "/snapshots/before"); err != nil {
		t.Errorf("RemoveAll of a label already gone = %v, want nothing", err)
	}
}

// A label is one name. A path into the snapshots is a path into something they
// do not have, and it is refused rather than filed under a name with a
// separator in it.
func TestALabelIsOneName(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})

	if err := graph.Write(t.Context(), "/snapshots/last/friday", nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Write(/snapshots/last/friday) = %v, want fs.ErrInvalid", err)
	}
}

// Going back to a label nothing was kept under is the store's fs.ErrNotExist,
// and it leaves the graph where it was.
func TestRestoringALabelNothingWasKeptUnderChangesNothing(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	if err := graph.Write(t.Context(), "/restore", []byte("monday")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Write(/restore) = %v, want fs.ErrNotExist", err)
	}
	if body := read(t, graph, "/docs/stack"); body != "Nerve uses Postgres" {
		t.Errorf("Read(/docs/stack) = %q, want the graph as it was", body)
	}
}

// A restore the record refused is a restore that did not happen here either,
// which is what every other change to this mount already promises.
func TestARestoreTheStoreRefusedIsARestoreTheGraphDidNotMake(t *testing.T) {
	store := newMemStore()
	graph := newGraph(t, store, statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/snapshots/before", "")
	write(t, graph, "/docs/stack", "Nerve uses Neo4j")

	store.refuse = errors.New("the bucket is not answering")

	if err := graph.Write(t.Context(), "/restore", []byte("before")); err == nil {
		t.Fatal("Write(/restore) = nil, want the store's refusal")
	}
	if body := read(t, graph, "/docs/stack"); body != "Nerve uses Neo4j" {
		t.Errorf("Read(/docs/stack) = %q, want the text the refused restore did not replace", body)
	}
}
