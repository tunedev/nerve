package vectorfs

import (
	"context"
	"io/fs"
	"time"
)

// DocStore is where the corpus itself lives: the bodies, and the mode and the
// instant recorded beside each one. It is the record of what is in the corpus,
// and the index beside it is derived from it — which is the whole reason the
// two are separate ports. A document is a document wherever it sits; a vector
// is only worth what the model that made it is worth, and it can be made again.
//
// A key is a flat name with no leading slash. Nothing here knows about
// directories, because a corpus has none: turning paths into keys and prefixes
// back into directories is FS's work, above this line.
//
// A store holds and hands back copies. The slice a caller gives Put is not the
// one kept, and the one Get returns is not the one held.
//
// A store stamps the time a write happened, because the store is what performs
// the write: a disk records when the file changed and an object store records
// the instant on the wire. What the port asks is only that a write moves the
// instant forward.
//
// Errors are the io/fs sentinels: a key with nothing under it is fs.ErrNotExist,
// whatever the disk or the wire called it.
type DocStore interface {
	// Head describes the document at key without fetching it. It is separate
	// from Get because Stat and a listing are what a caller does most, and
	// hauling a whole document back to report how big it is would make
	// describing a corpus cost what reading it costs.
	Head(ctx context.Context, key string) (Entry, error)

	// Get returns the document stored at key. Reading does not consume: the
	// same bytes come back to the next reader.
	Get(ctx context.Context, key string) ([]byte, error)

	// Put stores body at key with mode, replacing whatever was there.
	Put(ctx context.Context, key string, body []byte, mode fs.FileMode) error

	// Move stores what is at from at to instead, replacing what is at to and
	// failing with fs.ErrNotExist when nothing is at from. It carries the mode
	// across unchanged: a document that moved is not a document that was
	// written.
	//
	// It is one call so that a store able to move a document without its body
	// passing through this process does.
	Move(ctx context.Context, from, to string) error

	// Delete removes the document at key, failing with fs.ErrNotExist when
	// nothing is there.
	Delete(ctx context.Context, key string) error

	// DeleteAll removes every document whose key starts with prefix. A prefix
	// with nothing under it is not an error.
	DeleteAll(ctx context.Context, prefix string) error

	// List describes every document whose key starts with prefix, however deep,
	// in no particular order. An empty prefix is the whole corpus.
	//
	// A listing carries what the store can list, which is less than Head knows.
	// An object store's list call reports no metadata, so the mode a listing
	// reports there is the default rather than the bits a Chmod set. Head is
	// where those are read back, on either store.
	List(ctx context.Context, prefix string) ([]Entry, error)

	// Chmod sets the permission bits the document at key carries, without
	// touching its body.
	Chmod(ctx context.Context, key string, mode fs.FileMode) error
}

// Entry describes one document without being it: what a listing and a Stat are
// answered from.
type Entry struct {
	// Key is the flat name, without a leading slash.
	Key string
	// Size is the length of the body in bytes.
	Size int64
	// Mode is the permission bits it was stored with, with no directory bit:
	// nothing stored under a key is a directory.
	Mode fs.FileMode
	// Written is when the store last wrote the body.
	Written time.Time
}
