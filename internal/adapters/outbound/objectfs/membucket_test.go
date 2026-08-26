package objectfs_test

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

// memBucket is an object store held in a map. It is what makes the plugin's
// prefix semantics assertable without a container: the suite that runs against
// MinIO runs against this one offline, and the two have to agree.
//
// It is deliberately flat. Nothing here knows a directory, so a case that
// passes against it passes because the plugin turned prefixes into entries,
// not because the store did.
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
			listing.Objects = append(listing.Objects, describe(key, b.objects[key]))
			continue
		}
		seen[prefix+rest[:cut]+delimiter] = struct{}{}
	}

	listing.Prefixes = slices.Sorted(maps.Keys(seen))
	return listing, nil
}

func describe(key string, object stored) objectfs.Object {
	return objectfs.Object{
		Key:     key,
		Size:    int64(len(object.data)),
		Mode:    object.mode,
		ModTime: object.modTime,
	}
}
