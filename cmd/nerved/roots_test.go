package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// here is the workspace every case below stands in.
var here = workspace.Scope{User: "ada", Workspace: "hunt"}

// theAdvert is what a workspace uploads.
const theAdvert = "Senior Go engineer, remote, closes Friday.\n"

// rooted builds what main builds from the config document in body: the resolved
// config, and the table its mounts make.
func rooted(t *testing.T, body string) (config.Config, *vfs.MountTable) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "nerve.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(config.Sources{File: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	registry := vfs.NewRegistry(cfg.CallTimeoutDuration(), cfg.RetryPolicy(), cfg.BreakerPolicy())
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now),
		stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds: %v", err)
	}
	table, err := registry.Table(cfg.MountSpecs())
	if err != nil {
		t.Fatalf("build table: %v", err)
	}
	return cfg, table
}

// keyFile writes a signing key where a store can read it.
func keyFile(t *testing.T, key string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "download.key")
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

// resourcesOn is the config document a build that takes uploads runs on: a
// mount, the root it puts the resources under, and the file the key is read
// from.
func resourcesOn(t *testing.T, key string) (config.Config, *vfs.MountTable) {
	t.Helper()

	return rooted(t, `{
		"mounts":            [{"prefix": "/resources", "kind": "memfs"}],
		"resources-root":    "/resources",
		"download-key-file": "`+keyFile(t, key)+`"
	}`)
}

// upload puts theAdvert in store under name and finalises it.
func upload(t *testing.T, store *resource.Store, name string) {
	t.Helper()

	if _, err := store.Create(t.Context(), here, name, "text/plain"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Upload(t.Context(), here, name, strings.NewReader(theAdvert)); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, err := store.Complete(t.Context(), here, name); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// download redeems a token and reads what it grants.
func download(t *testing.T, store *resource.Store, token string) string {
	t.Helper()

	content, err := store.Download(t.Context(), token)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = content.Close() }()

	body, err := io.ReadAll(content)
	if err != nil {
		t.Fatalf("read the download: %v", err)
	}
	return string(body)
}

// The acceptance: a running process has a resource store. The root is a line of
// config, the storage under it is whatever that same document mounted, and a
// resource goes in and comes back out through a grant.
func TestAConfiguredRootGivesTheProcessAResourceStore(t *testing.T) {
	cfg, table := resourcesOn(t, "the-signing-key")

	resources, err := resourceStore(table, cfg.ResourcesRoot, cfg.DownloadKeyFile)
	if err != nil {
		t.Fatalf("resourceStore: %v", err)
	}
	if resources == nil {
		t.Fatal("resourceStore() = nil, want a store")
	}

	upload(t, resources, "advert.txt")
	grant, err := resources.SignedDownload(t.Context(), here, "advert.txt", time.Minute)
	if err != nil {
		t.Fatalf("SignedDownload: %v", err)
	}
	if got := download(t, resources, grant.Token); got != theAdvert {
		t.Errorf("Download = %q, want %q", got, theAdvert)
	}
}

// The key is a file so that it outlives the process holding it: a grant signed
// before a restart is still redeemable after one, and one signed by a process
// holding a different key grants nothing. A key made up at boot would pass
// every case that used one store and fail every holder of a live token.
func TestAGrantOutlivesTheProcessThatSignedIt(t *testing.T) {
	cfg, table := resourcesOn(t, "the-signing-key")

	before, err := resourceStore(table, cfg.ResourcesRoot, cfg.DownloadKeyFile)
	if err != nil {
		t.Fatalf("resourceStore: %v", err)
	}
	upload(t, before, "advert.txt")
	grant, err := before.SignedDownload(t.Context(), here, "advert.txt", time.Minute)
	if err != nil {
		t.Fatalf("SignedDownload: %v", err)
	}

	// The same key file read again is the same process booted again.
	after, err := resourceStore(table, cfg.ResourcesRoot, cfg.DownloadKeyFile)
	if err != nil {
		t.Fatalf("resourceStore after the restart: %v", err)
	}
	if got := download(t, after, grant.Token); got != theAdvert {
		t.Errorf("Download after the restart = %q, want %q", got, theAdvert)
	}

	elsewhere, err := resourceStore(table, cfg.ResourcesRoot, keyFile(t, "another-signing-key"))
	if err != nil {
		t.Fatalf("resourceStore elsewhere: %v", err)
	}
	if _, err := elsewhere.Download(t.Context(), grant.Token); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Download under another key = %v, want fs.ErrPermission", err)
	}
}

// A secret that cannot be read is a boot failure rather than a store that signs
// with nothing. Every spelling of "there is no key here" is refused.
func TestAKeyThatCannotBeReadRefusesToBoot(t *testing.T) {
	_, table := rooted(t, `{"mounts": [{"prefix": "/resources", "kind": "memfs"}]}`)

	cases := map[string]string{
		"no such file": filepath.Join(t.TempDir(), "absent.key"),
		"empty file":   keyFile(t, ""),
		"newline only": keyFile(t, "\n"),
	}

	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			store, err := resourceStore(table, "/resources", path)
			if err == nil {
				t.Fatalf("resourceStore() = %v, want an error", store)
			}
		})
	}
}

// A key written with a shell redirect carries a newline that is not part of it,
// so the same key typed two ways signs the same grants.
func TestATrailingNewlineIsNotPartOfTheKey(t *testing.T) {
	cfg, table := resourcesOn(t, "the-signing-key\n")

	written, err := resourceStore(table, cfg.ResourcesRoot, cfg.DownloadKeyFile)
	if err != nil {
		t.Fatalf("resourceStore: %v", err)
	}
	upload(t, written, "advert.txt")
	grant, err := written.SignedDownload(t.Context(), here, "advert.txt", time.Minute)
	if err != nil {
		t.Fatalf("SignedDownload: %v", err)
	}

	typed, err := resourceStore(table, cfg.ResourcesRoot, keyFile(t, "the-signing-key"))
	if err != nil {
		t.Fatalf("resourceStore: %v", err)
	}
	if got := download(t, typed, grant.Token); got != theAdvert {
		t.Errorf("Download under the key without the newline = %q, want %q", got, theAdvert)
	}
}

// A build that names no root holds no store and needs no secret. That is the
// checkout with no resources mount, and it boots.
func TestNoRootIsNoStoreRatherThanAnError(t *testing.T) {
	cfg, table := rooted(t, `{}`)

	resources, err := resourceStore(table, cfg.ResourcesRoot, cfg.DownloadKeyFile)
	if err != nil {
		t.Fatalf("resourceStore: %v", err)
	}
	if resources != nil {
		t.Errorf("resourceStore() = %v, want no store", resources)
	}

	scratch, err := scratchSpace(table, cfg.ScratchRoot)
	if err != nil {
		t.Fatalf("scratchSpace: %v", err)
	}
	if scratch != nil {
		t.Errorf("scratchSpace() = %v, want no space", scratch)
	}
}

// The scratch root is the other half of the same decision: a line of config, and
// a workspace's scratch state stands under it.
func TestAConfiguredRootGivesTheProcessAScratchSpace(t *testing.T) {
	cfg, table := rooted(t, `{
		"mounts":       [{"prefix": "/scratch", "kind": "memfs"}],
		"scratch-root": "/scratch"
	}`)

	scratch, err := scratchSpace(table, cfg.ScratchRoot)
	if err != nil {
		t.Fatalf("scratchSpace: %v", err)
	}
	if scratch == nil {
		t.Fatal("scratchSpace() = nil, want a space")
	}

	if err := scratch.Put(t.Context(), here, "run", "cursor", []byte("page 3")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	value, err := scratch.Get(t.Context(), here, "run", "cursor")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(value) != "page 3" {
		t.Errorf("Get = %q, want %q", value, "page 3")
	}
}
