package vectorfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
)

// ObjectDocs is a corpus in an object store: a key is an object name, and a
// listing is a prefix query. It is the second implementation of DocStore, which
// is what makes DocStore a port rather than an interface tax — nothing above
// this line changes, and vectorfs is the same plugin over either corpus.
//
// The bucket is objectfs's port rather than a second one of its own. An object
// store reached two ways would be two clients, two signings and two sets of
// wire semantics to keep in agreement; the dependency points from the corpus
// towards the object store because that is the direction the layering runs, and
// it stops at objectfs's port rather than at its S3 client.
//
// A bucket is not a corpus at one point, and it is answered here: a delete of
// nothing succeeds on the wire where the port promises fs.ErrNotExist.
type ObjectDocs struct {
	bucket objectfs.Bucket
}

// NewObjectDocs returns the corpus held in bucket.
func NewObjectDocs(bucket objectfs.Bucket) *ObjectDocs {
	return &ObjectDocs{bucket: bucket}
}

// Head describes key without fetching it: over a bucket this is a HEAD where
// Get is a GET of the whole document.
func (d *ObjectDocs) Head(ctx context.Context, key string) (Entry, error) {
	object, err := d.bucket.Head(ctx, key)
	if err != nil {
		return Entry{}, err
	}
	return entryOf(object), nil
}

// Get returns the document stored at key, read whole. A document moves whole
// through the port above, so there is nothing here to hand a stream to.
func (d *ObjectDocs) Get(ctx context.Context, key string) ([]byte, error) {
	body, _, err := d.bucket.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	// The content is in hand by the time the body closes, so a close error has
	// nothing left to tell the caller about what it asked for.
	defer func() { _ = body.Close() }()

	document, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("get %q: %w", key, err)
	}
	return document, nil
}

// Put stores body at key with mode. The store stamps the instant, which is what
// the port asks of it: the write happens there.
func (d *ObjectDocs) Put(ctx context.Context, key string, body []byte, mode fs.FileMode) error {
	return d.bucket.Put(ctx, key, body, mode.Perm())
}

// Move stores what is at from at to instead. The store does the copying, so a
// renamed document's body never travels through this process — which is the
// reason the port has a Move rather than leaving the plugin to fetch, store and
// delete.
//
// The Head is not a check that could be skipped: a copy replaces all of an
// object's metadata, so the mode has to be read before it can be carried over.
func (d *ObjectDocs) Move(ctx context.Context, from, to string) error {
	object, err := d.bucket.Head(ctx, from)
	if err != nil {
		return err
	}
	if err := d.bucket.Copy(ctx, from, to, object.Mode); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	if err := d.bucket.Delete(ctx, from); err != nil {
		return fmt.Errorf("move %q: %w", from, err)
	}
	return nil
}

// Delete removes the document at key. A bucket answers a delete of nothing with
// success, so what is not there is found here: the port promises fs.ErrNotExist,
// and a caller that removed a document it had already removed is told so.
func (d *ObjectDocs) Delete(ctx context.Context, key string) error {
	if _, err := d.bucket.Head(ctx, key); err != nil {
		return err
	}
	return d.bucket.Delete(ctx, key)
}

// DeleteAll removes every document under prefix. A prefix with nothing under it
// lists nothing and deletes nothing, which is not an error.
func (d *ObjectDocs) DeleteAll(ctx context.Context, prefix string) error {
	found, err := d.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, one := range found {
		if err := d.bucket.Delete(ctx, one.Key); err != nil {
			return fmt.Errorf("delete_all %q: %w", prefix, err)
		}
	}
	return nil
}

// List describes every document under prefix. The delimiter is empty because a
// corpus has no level to stop at: vectorfs folds the segments between
// separators into children itself, and a listing cut at the first one would
// hide the documents deeper down that a prefix exists because of.
//
// What a listing carries is what the store's list call carries. A bucket
// reports its own last-modified and no metadata, so the mode here is the
// default rather than the one a Chmod set; Head is where the bits a caller set
// are read back.
func (d *ObjectDocs) List(ctx context.Context, prefix string) ([]Entry, error) {
	listing, err := d.bucket.List(ctx, prefix, "")
	if err != nil {
		return nil, err
	}

	found := make([]Entry, len(listing.Objects))
	for i, object := range listing.Objects {
		found[i] = entryOf(object)
	}
	return found, nil
}

// Chmod sets the permission bits the document at key carries. A bucket has no
// modes, so it is a copy of the object onto itself with the metadata replaced,
// which is what keeps the body off the wire for a verb that does not touch it.
func (d *ObjectDocs) Chmod(ctx context.Context, key string, mode fs.FileMode) error {
	if _, err := d.bucket.Head(ctx, key); err != nil {
		return err
	}
	if err := d.bucket.Copy(ctx, key, key, mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", key, err)
	}
	return nil
}

// entryOf is what one object is as a document.
func entryOf(object objectfs.Object) Entry {
	return Entry{Key: object.Key, Size: object.Size, Mode: object.Mode.Perm(), Written: object.ModTime}
}
