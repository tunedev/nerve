package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	neturl "net/url"
	"os"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// endpointEnv turns the durability case on. It is the object store's own
// variable rather than one of nerved's: what it names is the store, whichever
// setting reaches it. `make up` starts the store, and `make test-store` runs
// this.
const endpointEnv = "NERVE_OBJECTFS_ENDPOINT"

// A URL is read as the bucket's own address, path-style: the credentials are
// the userinfo, the bucket is the path, the region is a query, and what is left
// is the endpoint the store answers on.
func TestAStoreURLIsReadAsOneBucketsAddress(t *testing.T) {
	cfg, err := bucket("https://AKIA:wJalrXUt@objects.example.com/nerve?region=eu-west-1")
	if err != nil {
		t.Fatalf("bucket() = %v", err)
	}

	want := objectfs.S3Config{
		Endpoint: "https://objects.example.com",
		Bucket:   "nerve",
		Region:   "eu-west-1",
		KeyID:    "AKIA",
		Secret:   "wJalrXUt",
	}
	if cfg != want {
		t.Errorf("bucket() = %+v, want %+v", cfg, want)
	}
}

// Every S3 signature is made for a region, and a store on the laptop has none
// to name, so a URL that names none is signed for the same region a mount that
// names none is.
func TestAStoreURLWithoutARegionSignsForTheDefault(t *testing.T) {
	cfg, err := bucket("http://nerve:nervenerve@127.0.0.1:9000/nerve")
	if err != nil {
		t.Fatalf("bucket() = %v", err)
	}
	if cfg.Region != objectfs.DefaultRegion {
		t.Errorf("region = %q, want the default %q", cfg.Region, objectfs.DefaultRegion)
	}
}

// No URL is the keyspace in memory, which is what a laptop with no bucket runs.
// What makes that observable is that it is not durable: a second process holds
// nothing the first one wrote.
func TestNoStoreURLIsAKeyspaceInMemory(t *testing.T) {
	ctx := context.Background()

	first, err := keyspace("")
	if err != nil {
		t.Fatalf("keyspace(\"\") = %v", err)
	}
	if err := first.Put(ctx, "sessions/abc", []byte("running"), 0o644); err != nil {
		t.Fatalf("Put = %v", err)
	}

	second, err := keyspace("")
	if err != nil {
		t.Fatalf("keyspace(\"\") = %v", err)
	}
	if _, err := second.Get(ctx, "sessions/abc"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get through a second keyspace in memory = %v, want fs.ErrNotExist", err)
	}
}

// The acceptance: a mount an operator configured is over a real bucket, and a
// value written through it is still there when the process that wrote it is
// gone. Two tables built the way main builds one are the two boots.
func TestAValueWrittenThroughAKvfsMountSurvivesARestart(t *testing.T) {
	url := storeURL(t)
	ctx := context.Background()

	written := boot(t, url)
	if err := written.Write(ctx, "/sessions/abc/state", []byte("running")); err != nil {
		t.Fatalf("Write(/sessions/abc/state) = %v", err)
	}

	restarted := boot(t, url)
	t.Cleanup(func() {
		if err := restarted.RemoveAll(context.WithoutCancel(ctx), "/sessions"); err != nil {
			t.Errorf("emptying the namespace = %v", err)
		}
	})

	content, err := restarted.Read(ctx, "/sessions/abc/state")
	if err != nil {
		t.Fatalf("Read(/sessions/abc/state) after a restart = %v", err)
	}
	if string(content) != "running" {
		t.Errorf("Read(/sessions/abc/state) after a restart = %q, want %q", content, "running")
	}
}

// boot is what main does with a store URL: build the keyspace, register the
// kinds over it, and resolve the mount table the config names.
func boot(t *testing.T, url string) *vfs.MountTable {
	t.Helper()

	store, err := keyspace(url)
	if err != nil {
		t.Fatalf("keyspace(%q) = %v", url, err)
	}

	registry := vfs.NewRegistry(10*time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", store, vfs.NewTraffic(time.Now)); err != nil {
		t.Fatalf("registerKinds() = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/sessions", Kind: kvfs.Kind, Settings: map[string]string{kvfs.NamespaceSetting: namespace(t)}},
	})
	if err != nil {
		t.Fatalf("Table(/sessions) = %v", err)
	}
	t.Cleanup(func() {
		if err := registry.Release(); err != nil {
			t.Errorf("Release() = %v", err)
		}
	})
	return table
}

// storeURL is the local store as one setting, or a skip. The client pools
// connections, and a pooled connection is a goroutine the leak check finds
// still running once the tests are over.
func storeURL(t *testing.T) string {
	t.Helper()

	endpoint := os.Getenv(endpointEnv)
	if endpoint == "" {
		t.Skipf("set %s to run against an object store; `make up` starts one", endpointEnv)
	}
	t.Cleanup(http.DefaultTransport.(*http.Transport).CloseIdleConnections)

	parsed, err := neturl.Parse(endpoint)
	if err != nil {
		t.Fatalf("%s = %q: %v", endpointEnv, endpoint, err)
	}
	parsed.User = neturl.UserPassword(setting("NERVE_OBJECTFS_ACCESS_KEY_ID", "nerve"), setting("NERVE_OBJECTFS_SECRET_ACCESS_KEY", "nervenerve"))
	parsed.Path = "/" + setting("NERVE_OBJECTFS_BUCKET", "nerve")
	parsed.RawQuery = regionQuery + "=" + setting("NERVE_OBJECTFS_REGION", objectfs.DefaultRegion)
	return parsed.String()
}

// setting reads one of the store's settings, falling back to what
// docker-compose.yml sets. They are development credentials and live there too.
func setting(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// namespace keeps a run's keys to itself, so a bucket shared with the plugin's
// own cases is not a bucket where two of them answer each other.
func namespace(t *testing.T) string {
	t.Helper()
	return "nerved-" + t.Name()
}
