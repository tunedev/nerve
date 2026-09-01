package queuefs

import (
	"errors"
	"io"
	"testing"
)

// The queue under each of these has no journal and no broker, because nothing
// here calls a verb that would reach one: what is under test is the lifetime of
// the two things the factory opened, not what the queue does with them.

// A queue the factory built holds a connection pool and a broker connection
// nothing else opened. The registry reaches them by collecting whichever plugin
// a factory returns implementing io.Closer, so a queue that is not one is a
// queue that holds both until the process ends.
func TestAQueueGivesBackWhatTheFactoryOpened(t *testing.T) {
	journal, broker := &spyPool{}, &spyConnection{}

	if err := releasable(t, journal, broker).Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

	if !journal.closed {
		t.Error("Close() left the journal pool open")
	}
	if !broker.closed {
		t.Error("Close() left the broker connection open")
	}
}

// A broker that will not close is reported and is not a reason to leave a
// database pool open.
func TestTheJournalIsGivenBackWhenTheBrokerRefuses(t *testing.T) {
	refusal := errors.New("connection already gone")
	journal, broker := &spyPool{}, &spyConnection{refuses: refusal}

	err := releasable(t, journal, broker).Close()
	if !errors.Is(err, refusal) {
		t.Errorf("Close() = %v, want %v", err, refusal)
	}
	if !journal.closed {
		t.Error("a broker that refused to close left the journal pool open")
	}
}

// Holding two resources changes nothing about what the mount table dispatches
// to: every verb and the per-path descriptor are still the queue's.
func TestAQueueHoldingItsResourcesIsStillTheQueue(t *testing.T) {
	fsys := holding(New("orders", nil, nil), &spyPool{}, &spyConnection{})

	if caps := fsys.Capabilities("/" + Jobs); !caps.ReadDestructive || !caps.ObjectStore {
		t.Errorf("Capabilities(%q) = %+v, want a read-destructive object store", Jobs, caps)
	}
	if caps := fsys.Capabilities("/" + Config); !caps.ReadOnly {
		t.Errorf("Capabilities(%q) = %+v, want read-only", Config, caps)
	}
}

// releasable is the factory's queue seen the way Registry.Plugin sees it: the
// value a factory returned, asked whether it holds anything to give back.
func releasable(t *testing.T, journal pool, broker connection) io.Closer {
	t.Helper()

	closer, collected := holding(New("orders", nil, nil), journal, broker).(io.Closer)
	if !collected {
		t.Fatal("the factory's queue is not an io.Closer, so Registry.Release cannot reach it")
	}
	return closer
}

// spyPool is a journal's pool that records being given back.
type spyPool struct{ closed bool }

func (p *spyPool) Close() { p.closed = true }

// spyConnection is a broker's connection that records being given back, and
// refuses when it is told to.
type spyConnection struct {
	closed  bool
	refuses error
}

func (c *spyConnection) Close() error {
	c.closed = true
	return c.refuses
}
