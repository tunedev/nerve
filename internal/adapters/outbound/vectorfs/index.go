package vectorfs

import (
	"context"
	"io/fs"
	"time"
)

// Index is where the corpus actually lives: the documents, the vectors beside
// them, and the one query that compares vectors to each other. pgvector is the
// shipped side and whatever else speaks these verbs is another, which is what
// keeps the semantics above this line assertable with nothing started.
//
// A key is a flat name with no leading slash. Nothing here knows about
// directories, because an index has none: turning paths into keys and prefixes
// back into directories is FS's work, above this line.
//
// A document is stored with the vector that was made from it, and the two are
// written together. Storing text and remembering to embed it afterwards would
// make a failure between the two into a document that is in the corpus and
// findable by nobody.
//
// A document with no text has no vector, and Nearest never returns it. There is
// nothing for a similarity to be a similarity to.
//
// Errors are the io/fs sentinels: a key with nothing under it is fs.ErrNotExist,
// whatever the driver called it.
type Index interface {
	// Put stores body at key with the vector made from it, replacing whatever
	// was there. A nil vector is a document with nothing to compare.
	Put(ctx context.Context, key string, body []byte, vector []float32, mode fs.FileMode) error

	// Get returns the document stored at key. Reading does not consume: the
	// same bytes come back to the next reader.
	Get(ctx context.Context, key string) ([]byte, error)

	// Peek describes the document at key without returning its body.
	Peek(ctx context.Context, key string) (Entry, error)

	// List describes every document whose key starts with prefix, however
	// deep, sorted by key. An empty prefix is the whole corpus.
	List(ctx context.Context, prefix string) ([]Entry, error)

	// Drop removes the document at key.
	Drop(ctx context.Context, key string) error

	// DropAll removes every document whose key starts with prefix. A prefix
	// with nothing under it is not an error.
	DropAll(ctx context.Context, prefix string) error

	// Move puts the document at from at to, replacing what is already there.
	// The vector travels with the document: a rename changes a name and not
	// what the document says.
	Move(ctx context.Context, from, to string) error

	// Chmod sets the permission bits the document at key carries.
	Chmod(ctx context.Context, key string, mode fs.FileMode) error

	// Nearest returns up to limit documents closest to vector, closest first.
	// A corpus with nothing comparable in it returns no hits, which is an
	// answer and not a failure.
	Nearest(ctx context.Context, vector []float32, limit int) ([]Hit, error)
}

// Entry describes one document without being it. Nothing here is the body:
// describing a corpus is what Stat and ReadDir do, and hauling every document
// out of the index to answer how big it is would make a listing cost what
// reading the whole corpus costs.
type Entry struct {
	// Key is the flat name, without a leading slash.
	Key string
	// Size is the length of the body in bytes.
	Size int64
	// Mode is the permission bits it was stored with.
	Mode fs.FileMode
	// Written is when the body last changed.
	Written time.Time
}

// Hit is one document a query matched: where it is and how close it was.
//
// Scores are comparable within one query and mean nothing between two, which is
// the same promise search.Hit makes. Larger is closer, so the order a caller
// reads is the order the slice is already in.
type Hit struct {
	Key   string
	Score float64
}
