package kvfs

import (
	"context"
	"io/fs"
	"time"
)

// Store is the flat keyspace kvfs speaks to. It is a port rather than a struct
// because there are two implementations: a keyspace in memory, and ObjectStore,
// where a key is an object name and a listing is a prefix query.
//
// Keys are the whole address. Nothing here knows about namespaces, mounts or
// paths: kvfs turns a path into a key and a store never turns one back.
//
// A store holds and hands back copies. The slice a caller gives Put is not the
// one kept, and the one Get returns is not the one held, so the port's promise
// that a write cannot be changed through the caller's slice is kept in one
// place rather than in every plugin over a store.
//
// A store stamps the time a write happened. It is the one thing kvfs does not
// decide, because the store is what performs the write: an object store records
// the instant on the wire and hands it back, and a plugin's clock has no say in
// it. What the port asks is only that a write moves the instant forward.
type Store interface {
	// Head describes what is stored under key without fetching it, failing with
	// fs.ErrNotExist when nothing is. It is separate from Get because Stat is
	// the verb a caller uses most, and fetching a whole value to report its
	// size is a GET where a HEAD would do.
	Head(ctx context.Context, key string) (Entry, error)

	// Get returns what is stored under key, failing with fs.ErrNotExist when
	// nothing is.
	Get(ctx context.Context, key string) (Value, error)

	// Put stores data under key with mode, replacing whatever was there.
	Put(ctx context.Context, key string, data []byte, mode fs.FileMode) error

	// Move stores what is under from under to instead, replacing whatever was
	// at to and failing with fs.ErrNotExist when nothing is at from. It carries
	// the mode and the instant across unchanged: a value that moved is not a
	// value that was written.
	//
	// It is one call so that a store able to move a value without its content
	// passing through this process does, and so that a store with a transaction
	// has somewhere to put one.
	Move(ctx context.Context, from, to string) error

	// Delete removes key, failing with fs.ErrNotExist when nothing is there.
	Delete(ctx context.Context, key string) error

	// List describes every key that starts with prefix, in no particular order.
	// A prefix nothing starts with lists nothing and is not an error.
	List(ctx context.Context, prefix string) ([]Entry, error)
}

// Value is one stored value: its content, and what the store recorded beside it.
//
// Mode is carried because a key-value store has no modes and the port above has
// a Chmod, so the store keeps the bits it is given. ModTime is not given: it is
// what the store stamped when the value was last written.
type Value struct {
	// Data is the content stored under the key.
	Data []byte
	// Mode is the permission bits Chmod set, with no directory bit: a key is
	// never a directory.
	Mode fs.FileMode
	// ModTime is when the store last wrote the value.
	ModTime time.Time
}

// Entry is one key described without its content: a store answers a listing
// with sizes, and reading every value to list a namespace would be one fetch
// per key.
type Entry struct {
	// Key is the whole key, as List was given it plus what follows the prefix.
	Key string
	// Size is the length of the value in bytes.
	Size int64
	// Mode and ModTime are what the store holds beside the content.
	Mode    fs.FileMode
	ModTime time.Time
}

// entry describes value under key, which is what Head and a listing hand back.
func entry(key string, value Value) Entry {
	return Entry{Key: key, Size: int64(len(value.Data)), Mode: value.Mode, ModTime: value.ModTime}
}
