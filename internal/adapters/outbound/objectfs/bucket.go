package objectfs

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// Bucket is the object store this plugin keeps its entries in. It is one port
// with two sides worth naming: MinIO on the laptop and a hosted store in the
// cloud, which is the substitution the platform is built around.
//
// A key is a flat name. Nothing here knows about directories, because an object
// store has none: turning paths into keys and prefixes back into directories is
// FS's work, above this line.
//
// Keys carry no leading slash. The empty key is the bucket itself and is not an
// object.
//
// Errors are the io/fs sentinels, the same ones the port above uses: a key that
// is not there is fs.ErrNotExist, whatever the wire called it.
type Bucket interface {
	// Head describes key without fetching it.
	Head(ctx context.Context, key string) (Object, error)

	// Get returns the content of key as a stream, which the caller closes, and
	// what Head would have said about it.
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)

	// Put stores data under key, replacing whatever was there. It takes the
	// content whole because every caller above already holds it whole; Get
	// streams because a caller reading an object need not.
	Put(ctx context.Context, key string, data []byte, mode fs.FileMode) error

	// Copy stores what is under from a second time under to, with mode in place
	// of the mode from carried. It is what keeps a rename and a mode change
	// from moving the content through this process.
	Copy(ctx context.Context, from, to string, mode fs.FileMode) error

	// Delete removes key. A key that is not there is not an error, because the
	// state the caller asked for is the state that holds.
	Delete(ctx context.Context, key string) error

	// List returns what sits under prefix. A delimiter of "/" stops at the next
	// separator and collects what it stopped at in Prefixes, which is how one
	// level of a tree is read out of a flat namespace. An empty delimiter
	// returns everything beneath, however deep.
	List(ctx context.Context, prefix, delimiter string) (Listing, error)
}

// Object is what a bucket knows about one key.
//
// Mode is here because the port above has a Chmod and an object store has no
// modes: an implementation keeps it as user metadata beside the content. It
// carries permission bits only — nothing stored under a key is a directory.
type Object struct {
	// Key is the flat name, without a leading slash.
	Key string
	// Size is the length of the content in bytes.
	Size int64
	// Mode is the permission bits the object was stored with.
	Mode fs.FileMode
	// ModTime is when the content was last stored.
	ModTime time.Time
}

// Listing is one answer to List: the keys under a prefix, and the prefixes a
// delimiter stopped at.
type Listing struct {
	// Objects are the keys that sit directly under the prefix, sorted.
	Objects []Object
	// Prefixes are the longer names a delimiter cut short, each ending in the
	// delimiter. They are empty when List was given none.
	Prefixes []string
}
