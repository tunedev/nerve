package queuefs_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
)

// errGone is what a broker that is not there says.
var errGone = errors.New("the broker is not there")

// The acceptance in one case: the broker is down for the whole of two writes
// and neither is lost. The journal has them, a reader can take them, and the
// first write after the broker comes back carries everything it missed, oldest
// first.
//
// This is why the record comes first. A write that published before it recorded
// would have nothing to carry later and nothing to hand the reader now.
func TestLosingTheBrokerLosesNoMessage(t *testing.T) {
	journal := newMemJournal()
	broker := newMemBroker()
	fsys := queuefs.New("orders", journal, queuefs.NewRelay(journal, broker, 0))

	broker.refuse(errGone)
	mustWrite(t, fsys, "/jobs/first", "one")
	mustWrite(t, fsys, "/jobs/second", "two")

	if told := broker.keys(); len(told) != 0 {
		t.Fatalf("the broker was told %v while it was gone, want nothing", told)
	}
	if content := mustRead(t, fsys, "/jobs/first"); content != "one" {
		t.Errorf("Read of a message the broker never heard about = %q, want %q", content, "one")
	}

	broker.refuse(nil)
	mustWrite(t, fsys, "/jobs/third", "three")

	want := []string{"first", "second", "third"}
	if told := broker.keys(); !slices.Equal(told, want) {
		t.Errorf("the broker was told %v once it came back, want %v", told, want)
	}
}

// A consumer that was not there is caught up from the journal and from nothing
// the broker kept: the messages it is told about were claimed before it
// arrived, so the broker itself has no record of them left to replay.
func TestANewConsumerIsBackfilledFromTheJournal(t *testing.T) {
	journal := newMemJournal()
	carried := newMemBroker()
	relay := queuefs.NewRelay(journal, carried, 0)
	fsys := queuefs.New("orders", journal, relay)

	for _, name := range []string{"first", "second", "third"} {
		mustWrite(t, fsys, "/jobs/"+name, name)
		mustRead(t, fsys, "/jobs/"+name)
	}

	arrived := newMemBroker()
	backfill := queuefs.NewRelay(journal, arrived, 0)

	reached, err := backfill.Backfill(t.Context(), 0)
	if err != nil {
		t.Fatalf("Backfill = %v", err)
	}

	want := []string{"first", "second", "third"}
	if told := arrived.keys(); !slices.Equal(told, want) {
		t.Errorf("the consumer that arrived late was told %v, want %v", told, want)
	}

	again, err := backfill.Backfill(t.Context(), reached)
	if err != nil {
		t.Fatalf("Backfill from %d = %v", reached, err)
	}
	if again != reached {
		t.Errorf("Backfill from %d reached %d, want it to stay put", reached, again)
	}
	if told := arrived.keys(); !slices.Equal(told, want) {
		t.Errorf("a second Backfill told the consumer %v, want %v", told, want)
	}
}

// Carrying is not backfilling: what the broker was told once it is not told
// again by the next write.
func TestTheBrokerIsToldOnce(t *testing.T) {
	journal := newMemJournal()
	broker := newMemBroker()
	fsys := queuefs.New("orders", journal, queuefs.NewRelay(journal, broker, 0))

	mustWrite(t, fsys, "/jobs/first", "one")
	mustWrite(t, fsys, "/jobs/second", "two")

	want := []string{"first", "second"}
	if told := broker.keys(); !slices.Equal(told, want) {
		t.Errorf("the broker was told %v, want %v", told, want)
	}
}

// Replacing a message the broker has already been told about makes it carry the
// new body: the record is what is published, so a message that changed before
// anyone took it is published as it now stands.
func TestReplacingAMessageCarriesItAgain(t *testing.T) {
	journal := newMemJournal()
	broker := newMemBroker()
	fsys := queuefs.New("orders", journal, queuefs.NewRelay(journal, broker, 0))

	mustWrite(t, fsys, "/jobs/only", "first")
	mustWrite(t, fsys, "/jobs/only", "second")

	told := broker.carried()
	if len(told) != 2 || string(told[1].Body) != "second" {
		t.Errorf("the broker was told %d times, last %q, want 2 and %q", len(told), lastBody(told), "second")
	}
}

// The relay reports the refusal that stopped it, so a caller that wants to know
// whether the broker is reachable can ask. A write does not: the message is
// recorded either way.
func TestCarryReportsTheRefusal(t *testing.T) {
	journal := newMemJournal()
	broker := newMemBroker()
	relay := queuefs.NewRelay(journal, broker, 0)
	fsys := queuefs.New("orders", journal, relay)

	broker.refuse(errGone)
	mustWrite(t, fsys, "/jobs/first", "one")

	carried, err := relay.Carry(t.Context())
	if carried != 0 || !errors.Is(err, errGone) {
		t.Errorf("Carry with the broker gone = %d, %v, want 0 and the refusal", carried, err)
	}
}

func lastBody(carried []queuefs.Carry) string {
	if len(carried) == 0 {
		return ""
	}
	return string(carried[len(carried)-1].Body)
}
