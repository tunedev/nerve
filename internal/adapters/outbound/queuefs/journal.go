package queuefs

import (
	"context"
	"io/fs"
	"time"
)

// Journal is where the queue's messages actually live: the system of record,
// Postgres in the shipped stack and whatever else speaks the same eight verbs
// otherwise. It is one port with a second side worth naming, which is what
// keeps the semantics above it assertable with nothing started.
//
// A key is a flat name with no leading slash. Nothing here knows about
// directories, because a queue has none: turning paths into keys and prefixes
// back into directories is FS's work, above this line.
//
// Only pending messages are addressable. A message a reader claimed is gone as
// far as every verb here is concerned — but not gone from the journal, which is
// what lets a consumer that arrives later be backfilled from it.
//
// Errors are the io/fs sentinels: a key with no pending message under it is
// fs.ErrNotExist, whatever the driver called it.
type Journal interface {
	// Record makes key pending with this body and mode, replacing the pending
	// message already under it. It records in the same statement that the
	// broker has not been told, so a message and the intent to publish it are
	// committed together or not at all.
	Record(ctx context.Context, key string, body []byte, mode fs.FileMode) error

	// Claim takes the pending message under key out of the queue and hands its
	// body back. What it returns is gone for the next reader.
	Claim(ctx context.Context, key string) ([]byte, error)

	// Peek describes the pending message under key without consuming it.
	Peek(ctx context.Context, key string) (Entry, error)

	// List returns every pending message whose key starts with prefix, however
	// deep, sorted by key. An empty prefix is the whole queue.
	List(ctx context.Context, prefix string) ([]Entry, error)

	// Drop removes the pending message under key without anyone consuming it.
	Drop(ctx context.Context, key string) error

	// DropAll removes every pending message whose key starts with prefix. A
	// prefix with nothing under it is not an error.
	DropAll(ctx context.Context, prefix string) error

	// Move puts the pending message under from at to, replacing the pending
	// message already there.
	Move(ctx context.Context, from, to string) error

	// Chmod sets the permission bits the pending message under key carries.
	Chmod(ctx context.Context, key string, mode fs.FileMode) error
}

// Entry describes one pending message without being it. Nothing here is the
// body: describing a queue is what Stat and ReadDir do, and hauling every
// message out of the journal to answer how big it is would make a listing cost
// what draining the queue costs.
type Entry struct {
	// Key is the flat name, without a leading slash.
	Key string
	// Size is the length of the body in bytes.
	Size int64
	// Mode is the permission bits it was recorded with.
	Mode fs.FileMode
	// Recorded is when the body was last written.
	Recorded time.Time
}

// Outbox is the same journal seen by the one thing that reads it for a
// different reason: the relay, which asks what the broker has not been told and
// says when it has been.
//
// It is a second port over one store rather than three more methods on Journal,
// because the relay has no business claiming or dropping a message and a port
// is written for the consumer that holds it.
type Outbox interface {
	// Uncarried returns up to limit messages recorded but not yet carried to
	// the broker, oldest first.
	Uncarried(ctx context.Context, limit int) ([]Carry, error)

	// Carried records that the broker has been told about these messages.
	Carried(ctx context.Context, ids []int64) error

	// Recorded returns up to limit messages recorded after id, oldest first,
	// whether the broker was told about them or not and whether a reader
	// claimed them or not. It is what backfills a consumer that was not there
	// at the time, and it reads the journal rather than anything the broker
	// kept.
	Recorded(ctx context.Context, after int64, limit int) ([]Carry, error)
}

// Carry is one message on its way to the broker. It has an identity the journal
// assigned, which is what the relay resumes from and what the queue's own key
// cannot be: a key is reused every time a message under it is replaced.
type Carry struct {
	// ID orders the journal and is unique within it.
	ID int64
	// Key is the name the message was recorded under.
	Key string
	// Body is the message.
	Body []byte
}
