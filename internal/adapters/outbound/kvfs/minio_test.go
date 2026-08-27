package kvfs_test

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// endpointEnv turns this file on, and is the object store's own variable rather
// than one of kvfs's: what it names is the store, whichever plugin reaches it.
// Nothing here runs without one, which is what keeps the rest of the package
// true on a plane — the keyspace over a bucket in a map asserts the semantics,
// and this asserts that the wire keeps the same promises.
//
// `make up` starts the store these defaults point at, and `make test-store`
// runs this.
const endpointEnv = "NERVE_OBJECTFS_ENDPOINT"

// setting reads one of the store's settings, falling back to what
// docker-compose.yml sets. They are development credentials and live there too.
func setting(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// bucket is the real store, or a skip. The client pools connections, and a
// pooled connection is a goroutine the leak check finds still running once the
// tests are over.
func bucket(t *testing.T) objectfs.Bucket {
	t.Helper()

	endpoint := os.Getenv(endpointEnv)
	if endpoint == "" {
		t.Skipf("set %s to run against an object store; `make up` starts one", endpointEnv)
	}

	store, err := objectfs.NewS3(objectfs.S3Config{
		Endpoint: endpoint,
		Bucket:   setting("NERVE_OBJECTFS_BUCKET", "nerve"),
		Region:   setting("NERVE_OBJECTFS_REGION", "us-east-1"),
		KeyID:    setting("NERVE_OBJECTFS_ACCESS_KEY_ID", "nerve"),
		Secret:   setting("NERVE_OBJECTFS_SECRET_ACCESS_KEY", "nervenerve"),
	})
	if err != nil {
		t.Fatalf("NewS3 = %v", err)
	}
	t.Cleanup(http.DefaultTransport.(*http.Transport).CloseIdleConnections)

	return store
}

// The port's promises, against the store rather than against a map. What it
// catches is what no fake can be trusted for: a bucket's own answer to a delete
// of nothing, and whether a copy on the wire really carries the instant across
// so that a move is not a write.
func TestStorePortAgainstAnObjectStore(t *testing.T) {
	store := bucket(t)
	runStoreCases(t, func(t *testing.T) kvfs.Store {
		scope := &scoped{bucket: store, prefix: sanitise(t.Name()) + "/"}
		t.Cleanup(func() { scope.empty(t) })
		return kvfs.NewObjectStore(scope)
	})
}

// The suite is one test set every plugin runs, and the plugin over a keyspace
// on the wire is a plugin like any other: the same cases, held to the same
// descriptors, against MinIO instead of a map.
//
// The namespace is the scope. A mount is a part of a keyspace by definition, so
// a case that holds one to itself needs nothing the plugin does not already do.
func TestConformanceAgainstAnObjectStore(t *testing.T) {
	store := kvfs.NewObjectStore(bucket(t))

	vfstest.Run(t, vfstest.Plugin{
		New: func(t *testing.T) vfs.FileSystem {
			fsys := kvfs.New(store, sanitise(t.Name()))
			t.Cleanup(func() {
				if err := fsys.RemoveAll(context.WithoutCancel(t.Context()), "/"); err != nil {
					t.Errorf("emptying the namespace = %v", err)
				}
			})
			return fsys
		},
	})
}

// scoped is one bucket seen as several: every key it passes on carries a prefix
// of its own, so cases that share a store do not share a keyspace. The port
// cases below the plugin have no namespace to scope them, which is what this
// stands in for.
type scoped struct {
	bucket objectfs.Bucket
	prefix string
}

func (s *scoped) Head(ctx context.Context, key string) (objectfs.Object, error) {
	object, err := s.bucket.Head(ctx, s.prefix+key)
	return s.unscope(object), err
}

func (s *scoped) Get(ctx context.Context, key string) (io.ReadCloser, objectfs.Object, error) {
	stream, object, err := s.bucket.Get(ctx, s.prefix+key)
	return stream, s.unscope(object), err
}

func (s *scoped) Put(ctx context.Context, key string, data []byte, mode fs.FileMode) error {
	return s.bucket.Put(ctx, s.prefix+key, data, mode)
}

func (s *scoped) Copy(ctx context.Context, from, to string, mode fs.FileMode) error {
	return s.bucket.Copy(ctx, s.prefix+from, s.prefix+to, mode)
}

func (s *scoped) Delete(ctx context.Context, key string) error {
	return s.bucket.Delete(ctx, s.prefix+key)
}

func (s *scoped) List(ctx context.Context, prefix, delimiter string) (objectfs.Listing, error) {
	listing, err := s.bucket.List(ctx, s.prefix+prefix, delimiter)
	if err != nil {
		return objectfs.Listing{}, err
	}

	for i := range listing.Objects {
		listing.Objects[i] = s.unscope(listing.Objects[i])
	}
	for i, common := range listing.Prefixes {
		listing.Prefixes[i] = strings.TrimPrefix(common, s.prefix)
	}
	return listing, nil
}

// unscope puts a key back the way the store above spelled it.
func (s *scoped) unscope(object objectfs.Object) objectfs.Object {
	object.Key = strings.TrimPrefix(object.Key, s.prefix)
	return object
}

// empty deletes everything the case stored, so the next run starts where this
// one did.
func (s *scoped) empty(t *testing.T) {
	t.Helper()

	listing, err := s.bucket.List(context.Background(), s.prefix, "")
	if err != nil {
		t.Errorf("listing %q to empty it = %v", s.prefix, err)
		return
	}
	for _, object := range listing.Objects {
		if err := s.bucket.Delete(context.Background(), object.Key); err != nil {
			t.Errorf("Delete(%q) = %v", object.Key, err)
		}
	}
}

// sanitise turns a test name into something a key can carry, since a case name
// holds separators and spaces that would read as prefixes of their own.
func sanitise(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, name)
}
