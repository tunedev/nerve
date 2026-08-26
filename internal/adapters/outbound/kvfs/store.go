package kvfs

import (
	"context"
	"io/fs"
	"time"
)

// Store is the flat keyspace kvfs speaks to. It is a port rather than a struct
// because there is a second implementation to name: an object-backed store,
// where a key is an object name and a listing is a prefix query.
//
// Keys are the whole address. Nothing here knows about namespaces, mounts or
// paths: kvfs turns a path into a key and a store never turns one back.
//
// A store holds and hands back copies. The slice a caller gives Put is not the
// one kept, and the one Get returns is not the one held, so the port's promise
// that a write cannot be changed through the caller's slice is kept in one
// place rather than in every plugin over a store.
type Store interface {
	// Get returns what is stored under key, failing with fs.ErrNotExist when
	// nothing is.
	Get(ctx context.Context, key string) (Value, error)

	// Put stores value under key, replacing whatever was there.
	Put(ctx context.Context, key string, value Value) error

	// Delete removes key, failing with fs.ErrNotExist when nothing is there.
	Delete(ctx context.Context, key string) error

	// List describes every key that starts with prefix, in no particular order.
	// A prefix nothing starts with lists nothing and is not an error.
	List(ctx context.Context, prefix string) ([]Entry, error)
}

// Value is one stored value.
//
// Mode and ModTime are carried because a key-value store has neither and the
// port promises both: kvfs is where the port's vocabulary is spoken, so it is
// kvfs that decides them and the store that keeps them.
type Value struct {
	// Data is the content stored under the key.
	Data []byte
	// Mode is the permission bits Chmod set, with no directory bit: a key is
	// never a directory.
	Mode fs.FileMode
	// ModTime is when the value was last stored.
	ModTime time.Time
}

// Entry is one key a listing found, described without handing back its content:
// a store answers a listing with sizes, and reading every value to list a
// namespace would be one fetch per key.
type Entry struct {
	// Key is the whole key, as List was given it plus what follows the prefix.
	Key string
	// Size is the length of the value in bytes.
	Size int64
	// Mode and ModTime are what was stored with the value.
	Mode    fs.FileMode
	ModTime time.Time
}

// entry describes value under key, which is what a listing hands back.
func entry(key string, value Value) Entry {
	return Entry{Key: key, Size: int64(len(value.Data)), Mode: value.Mode, ModTime: value.ModTime}
}
