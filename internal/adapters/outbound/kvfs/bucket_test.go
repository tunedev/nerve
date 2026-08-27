package kvfs_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
)

// listedMode is the mode a listing reports. A bucket's list call carries an
// object's key, size and time and not the metadata beside it, so the mode a
// Chmod set is not in a listing and the default stands in for it.
const listedMode = fs.FileMode(0o644)

// memBucket is an object store held in a map, and what lets the object-backed
// keyspace be asserted with nothing running. It is not the plugin's own fake:
// objectfs has one too, and this one exists so that a case here fails for a
// reason in kvfs.
//
// It answers the way the wire answers rather than the way a map could. A delete
// of nothing succeeds, a listing reports the default mode and a time rounded to
// the second, and nothing here knows a directory. A store that told the truth
// where S3 does not would let a defect pass offline and surface against MinIO.
type memBucket struct {
	mu      sync.Mutex
	objects map[string]stored
}

// stored is one object: its content, the mode it was stored with, and when.
type stored struct {
	data    []byte
	mode    fs.FileMode
	modTime time.Time
}

func newMemBucket() *memBucket {
	return &memBucket{objects: make(map[string]stored)}
}

func (b *memBucket) Head(_ context.Context, key string) (objectfs.Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	object, there := b.objects[key]
	if !there {
		return objectfs.Object{}, fmt.Errorf("head %q: %w", key, fs.ErrNotExist)
	}
	return describe(key, object), nil
}

func (b *memBucket) Get(_ context.Context, key string) (io.ReadCloser, objectfs.Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	object, there := b.objects[key]
	if !there {
		return nil, objectfs.Object{}, fmt.Errorf("get %q: %w", key, fs.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(object.data))), describe(key, object), nil
}

func (b *memBucket) Put(_ context.Context, key string, data []byte, mode fs.FileMode) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.objects[key] = stored{data: bytes.Clone(data), mode: mode.Perm(), modTime: time.Now()}
	return nil
}

func (b *memBucket) Copy(_ context.Context, from, to string, mode fs.FileMode) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	object, there := b.objects[from]
	if !there {
		return fmt.Errorf("copy %q: %w", from, fs.ErrNotExist)
	}
	object.mode = mode.Perm()
	b.objects[to] = object
	return nil
}

// Delete removes key, and removing what is not there succeeds. That is what the
// wire says, and it is why the keyspace over a bucket looks before it deletes.
func (b *memBucket) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.objects, key)
	return nil
}

func (b *memBucket) List(_ context.Context, prefix, delimiter string) (objectfs.Listing, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	listing := objectfs.Listing{Objects: nil, Prefixes: nil}
	seen := make(map[string]struct{})

	for _, key := range slices.Sorted(maps.Keys(b.objects)) {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := key[len(prefix):]
		cut := strings.Index(rest, delimiter)
		if delimiter == "" || cut < 0 {
			listing.Objects = append(listing.Objects, listed(key, b.objects[key]))
			continue
		}
		seen[prefix+rest[:cut]+delimiter] = struct{}{}
	}

	listing.Prefixes = slices.Sorted(maps.Keys(seen))
	return listing, nil
}

// describe is what a bucket says about a key it fetched or described, read from
// the metadata beside the content.
func describe(key string, object stored) objectfs.Object {
	return objectfs.Object{
		Key:     key,
		Size:    int64(len(object.data)),
		Mode:    object.mode,
		ModTime: object.modTime,
	}
}

// listed is what a bucket says about a key it listed, which is less: a store's
// list call reports its own last-modified, to the second, and no metadata.
func listed(key string, object stored) objectfs.Object {
	return objectfs.Object{
		Key:     key,
		Size:    int64(len(object.data)),
		Mode:    listedMode,
		ModTime: object.modTime.Truncate(time.Second),
	}
}
