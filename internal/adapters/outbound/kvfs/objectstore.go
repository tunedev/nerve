package kvfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
)

// ObjectStore is a keyspace in an object store: a key is an object name, and a
// listing is a prefix query. It is the second implementation of Store, which is
// what makes Store a port rather than an interface tax — nothing above this
// line changes, and kvfs is the same plugin over either keyspace.
//
// The bucket is objectfs's port rather than a second one of its own. An object
// store reached two ways would be two clients, two signings and two sets of
// wire semantics to keep in agreement; the dependency points from the key-value
// store towards the object store because that is the direction the layering
// runs, and it stops at objectfs's port rather than at its S3 client.
//
// A bucket is not a keyspace at two points, and both are answered here. A
// delete of nothing succeeds on the wire where the port promises fs.ErrNotExist,
// and a listing over the wire describes an object from what the listing carries
// rather than from the metadata beside it.
type ObjectStore struct {
	bucket objectfs.Bucket
}

// NewObjectStore returns the keyspace held in bucket.
func NewObjectStore(bucket objectfs.Bucket) *ObjectStore {
	return &ObjectStore{bucket: bucket}
}

// Head describes key without fetching it. This is the round trip the port grew
// a verb for: over a bucket it is a HEAD where Get is a GET of the whole value.
func (s *ObjectStore) Head(ctx context.Context, key string) (Entry, error) {
	object, err := s.bucket.Head(ctx, key)
	if err != nil {
		return Entry{}, err
	}
	return described(object), nil
}

// Get returns what is stored under key, read whole. A value moves whole through
// the port above, so there is nothing here to hand a stream to.
func (s *ObjectStore) Get(ctx context.Context, key string) (Value, error) {
	body, object, err := s.bucket.Get(ctx, key)
	if err != nil {
		return Value{}, err
	}
	// The content is in hand by the time the body closes, so a close error has
	// nothing left to tell the caller about what it asked for.
	defer func() { _ = body.Close() }()

	data, err := io.ReadAll(body)
	if err != nil {
		return Value{}, fmt.Errorf("get %q: %w", key, err)
	}
	return Value{Data: data, Mode: object.Mode, ModTime: object.ModTime}, nil
}

// Put stores data under key with mode. The store stamps the instant, which is
// what the port asks of it: the write happens there.
func (s *ObjectStore) Put(ctx context.Context, key string, data []byte, mode fs.FileMode) error {
	return s.bucket.Put(ctx, key, data, mode)
}

// Move stores what is under from under to instead. The store does the copying,
// so the content never travels through this process to be renamed — which is
// the reason the port has a Move rather than leaving the plugin to fetch, store
// and delete.
//
// The Head is not a check that could be skipped: a copy replaces all of an
// object's metadata, so the mode has to be read before it can be carried over.
func (s *ObjectStore) Move(ctx context.Context, from, to string) error {
	object, err := s.bucket.Head(ctx, from)
	if err != nil {
		return err
	}
	if err := s.bucket.Copy(ctx, from, to, object.Mode); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	if err := s.bucket.Delete(ctx, from); err != nil {
		return fmt.Errorf("move %q: %w", from, err)
	}
	return nil
}

// Delete removes key. A bucket answers a delete of nothing with success, so
// what is not there is found here: the port promises fs.ErrNotExist, and a
// caller that removed a session it had already removed is told so.
func (s *ObjectStore) Delete(ctx context.Context, key string) error {
	if _, err := s.bucket.Head(ctx, key); err != nil {
		return err
	}
	return s.bucket.Delete(ctx, key)
}

// List describes every key under prefix. The delimiter is empty because a
// keyspace has no level to stop at: kvfs folds the segments between separators
// into children itself, and a listing cut at the first one would hide the keys
// deeper down that a prefix exists because of.
func (s *ObjectStore) List(ctx context.Context, prefix string) ([]Entry, error) {
	listing, err := s.bucket.List(ctx, prefix, "")
	if err != nil {
		return nil, err
	}

	found := make([]Entry, len(listing.Objects))
	for i, object := range listing.Objects {
		found[i] = described(object)
	}
	return found, nil
}

// described is what one object is as a listed key.
func described(object objectfs.Object) Entry {
	return Entry{Key: object.Key, Size: object.Size, Mode: object.Mode, ModTime: object.ModTime}
}
