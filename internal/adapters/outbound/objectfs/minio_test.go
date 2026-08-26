package objectfs_test

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// endpointEnv turns this file on. Nothing here runs without a store to talk to,
// which is what keeps the rest of the package's tests true on a plane: the
// semantics are asserted against a bucket in a map, and this asserts that the
// wire keeps the same promises.
//
// `make up` starts the store these defaults point at.
const endpointEnv = "NERVE_OBJECTFS_ENDPOINT"

// The rest are what docker-compose.yml sets, overridable for a store somewhere
// else. They are development credentials and live in that file too.
func setting(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// minio is the plugin over the real store, scoped to a prefix of its own and
// emptied when the case ends. One bucket serves every case that way, which is
// what "a filesystem with nothing in it" has to mean when the filesystem
// outlives the process.
func minio(t *testing.T) vfs.FileSystem {
	t.Helper()

	endpoint := os.Getenv(endpointEnv)
	if endpoint == "" {
		t.Skipf("set %s to run against an object store; `make up` starts one", endpointEnv)
	}

	bucket, err := objectfs.NewS3(objectfs.S3Config{
		Endpoint: endpoint,
		Bucket:   setting("NERVE_OBJECTFS_BUCKET", "nerve"),
		Region:   setting("NERVE_OBJECTFS_REGION", "us-east-1"),
		KeyID:    setting("NERVE_OBJECTFS_ACCESS_KEY_ID", "nerve"),
		Secret:   setting("NERVE_OBJECTFS_SECRET_ACCESS_KEY", "nervenerve"),
	})
	if err != nil {
		t.Fatalf("NewS3 = %v", err)
	}

	scope := &scoped{bucket: bucket, prefix: sanitise(t.Name()) + "/"}
	t.Cleanup(func() { scope.empty(t) })
	// The client pools connections, and a pooled connection is a goroutine the
	// leak check finds still running once the tests are over.
	t.Cleanup(http.DefaultTransport.(*http.Transport).CloseIdleConnections)

	return objectfs.New(scope)
}

// The suite is one test set every plugin runs, and an object store over the
// wire is a plugin like any other: the same cases, held to the same
// descriptors, against MinIO instead of a map.
func TestConformanceAgainstAnObjectStore(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New:  minio,
		Root: "",
	})
}

// A mount reaches the store through the settings an operator wrote, so the
// settings are exercised rather than only the constructor they feed.
func TestFactoryBuildsAMountThatReachesTheStore(t *testing.T) {
	endpoint := os.Getenv(endpointEnv)
	if endpoint == "" {
		t.Skipf("set %s to run against an object store; `make up` starts one", endpointEnv)
	}
	t.Cleanup(http.DefaultTransport.(*http.Transport).CloseIdleConnections)

	fsys, err := objectfs.Factory(map[string]string{
		"endpoint":          endpoint,
		"bucket":            setting("NERVE_OBJECTFS_BUCKET", "nerve"),
		"access_key_id":     setting("NERVE_OBJECTFS_ACCESS_KEY_ID", "nerve"),
		"secret_access_key": setting("NERVE_OBJECTFS_SECRET_ACCESS_KEY", "nervenerve"),
	})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	name := "/" + sanitise(t.Name()) + "/notes"
	if err := fsys.Write(t.Context(), name, []byte("stored")); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
	t.Cleanup(func() {
		if err := fsys.Remove(context.WithoutCancel(t.Context()), name); err != nil {
			t.Errorf("Remove(%q) = %v", name, err)
		}
	})

	content, err := fsys.Read(t.Context(), name)
	if err != nil || string(content) != "stored" {
		t.Errorf("Read(%q) = %q, %v, want %q", name, content, err, "stored")
	}
}

// scoped is one bucket seen as several: every key it passes on carries a prefix
// of its own, so cases that share a store do not share a namespace.
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

// unscope puts a key back the way the plugin above spelled it.
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
