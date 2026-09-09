package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/streamrotatefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The composition root is the only place that knows a concrete plugin, so it is
// the only place a kind can be registered — and a kind the config can name is
// the whole point of the registry.
func TestRegisterKindsMakesMemfsMountable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/scratch", Kind: "memfs"}})
	if err != nil {
		t.Fatalf("Table(/scratch) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/scratch/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/scratch/notes) = %v", err)
	}
	content, err := table.Read(ctx, "/scratch/notes")
	if err != nil {
		t.Fatalf("Read(/scratch/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/scratch/notes) = %q, want %q", content, "text")
	}
}

// What the composition root is answerable for here is that the kind a config
// can name is there, that the setting it declares is the one an operator is
// allowed to read back, and that a mount naming no database file is refused
// while an operator is still watching. None of that opens a file.
func TestRegisterKindsDeclaresSqlfsAndItsDatabaseFile(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	if public := registry.Public(sqlfs.Kind); !slices.Equal(public, []string{sqlfs.PathSetting}) {
		t.Errorf("Public(sqlfs) = %v, want [%s]", public, sqlfs.PathSetting)
	}

	_, err := registry.Table([]vfs.MountSpec{{Prefix: "/scratch", Kind: sqlfs.Kind}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Table(sqlfs with no database file) = %v, want fs.ErrInvalid", err)
	}
}

// memfs takes no settings, so it declares none safe to read back and the
// effective-config surface has nothing of its to show.
func TestMemfsDeclaresNoPublicSettings(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	if public := registry.Public("memfs"); len(public) != 0 {
		t.Errorf("Public(memfs) = %v, want none", public)
	}
}

// localfs is the second kind, and the point of a registry is that a second one
// is a line here rather than an edit to a switch.
func TestRegisterKindsMakesLocalfsMountable(t *testing.T) {
	root := t.TempDir()
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/disk", Kind: "localfs", Settings: map[string]string{"root": root}}})
	if err != nil {
		t.Fatalf("Table(/disk) = %v", err)
	}

	if err := table.Write(context.Background(), "/disk/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/disk/notes) = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes"))
	if err != nil {
		t.Fatalf("reading the host file = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("the host file = %q, want %q", content, "text")
	}
}

// A host directory is not a credential, and an operator reading the effective
// config has to be able to see which disk a mount is on.
func TestLocalfsDeclaresItsRootPublic(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	if public := registry.Public("localfs"); !slices.Equal(public, []string{"root"}) {
		t.Errorf("Public(localfs) = %v, want [root]", public)
	}
}

// A kvfs mount is a namespaced part of the one keyspace the composition root
// owns, so two mounts the config names do not answer each other's keys.
func TestRegisterKindsMakesKvfsMountable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/sessions", Kind: "kvfs", Settings: map[string]string{"namespace": "sessions"}},
		{Prefix: "/agents", Kind: "kvfs", Settings: map[string]string{"namespace": "agents"}},
	})
	if err != nil {
		t.Fatalf("Table(/sessions, /agents) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/sessions/abc/state", []byte("running")); err != nil {
		t.Fatalf("Write(/sessions/abc/state) = %v", err)
	}
	content, err := table.Read(ctx, "/sessions/abc/state")
	if err != nil {
		t.Fatalf("Read(/sessions/abc/state) = %v", err)
	}
	if string(content) != "running" {
		t.Errorf("Read(/sessions/abc/state) = %q, want %q", content, "running")
	}

	if _, err := table.Read(ctx, "/agents/abc/state"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/agents/abc/state) = %v, want fs.ErrNotExist", err)
	}
}

// The namespace is what an operator reads back to know which keys a mount is
// over, so it is declared public where a credential would not be.
func TestKvfsDeclaresItsNamespacePublic(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	if public := registry.Public("kvfs"); !slices.Equal(public, []string{"namespace"}) {
		t.Errorf("Public(kvfs) = %v, want [namespace]", public)
	}
}

// objectfs reaches a store an operator names, and the two settings that reach
// it are credentials. Declaring only the other three is what makes them
// redacted everywhere the effective config is read back.
func TestObjectfsDeclaresOnlyWhatIsNotACredential(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	want := []string{"endpoint", "bucket", "region"}
	if public := registry.Public("objectfs"); !slices.Equal(public, want) {
		t.Errorf("Public(objectfs) = %v, want %v", public, want)
	}
	for _, credential := range []string{"access_key_id", "secret_access_key"} {
		if slices.Contains(registry.Public("objectfs"), credential) {
			t.Errorf("Public(objectfs) names %q, want it redacted", credential)
		}
	}
}

// A mount of a kind the build has is built at boot, so a misspelled setting or
// a missing credential is a boot failure an operator can still act on.
func TestObjectfsMountRefusesSettingsItCannotHonour(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	spec := vfs.MountSpec{Prefix: "/objects", Kind: "objectfs", Settings: map[string]string{"bucket": "nerve"}}
	if _, err := registry.Table([]vfs.MountSpec{spec}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Table with an unreachable objectfs mount = %v, want fs.ErrInvalid", err)
	}
}

// The acceptance for the utility kinds is that each one mounts and answers
// stat, so they are asserted together: a build that has them has them all, and
// a kind that stops mounting fails here rather than in whatever was using it.
func TestEveryUtilityKindMountsAndAnswersStat(t *testing.T) {
	specs := []vfs.MountSpec{
		{Prefix: "/hello", Kind: "hellofs"},
		{Prefix: "/heartbeat", Kind: "heartbeatfs"},
		{Prefix: "/serverinfo", Kind: "serverinfofs"},
		{Prefix: "/dev", Kind: "devfs"},
		{Prefix: "/origin", Kind: "httpfs", Settings: map[string]string{"base": "http://example.invalid"}},
		{Prefix: "/alias", Kind: "proxyfs", Settings: map[string]string{"target_kind": "memfs"}},
	}
	entries := map[string]string{
		"/hello":      "/hello/greeting",
		"/heartbeat":  "/heartbeat/beat",
		"/serverinfo": "/serverinfo/version",
		"/dev":        "/dev/null",
	}

	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table(specs)
	if err != nil {
		t.Fatalf("Table(the utility mounts) = %v", err)
	}

	ctx := context.Background()
	for prefix, entry := range entries {
		info, err := table.Stat(ctx, prefix)
		if err != nil {
			t.Errorf("Stat(%q) = %v, want the mount", prefix, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Stat(%q).IsDir() = false, want the mount's root", prefix)
		}
		if _, err := table.Stat(ctx, entry); err != nil {
			t.Errorf("Stat(%q) = %v, want the entry", entry, err)
		}
	}

	// The proxy is stat'd through the table, over a target the config named.
	if info, err := table.Stat(ctx, "/alias"); err != nil || !info.IsDir() {
		t.Errorf("Stat(/alias) = %+v, %v, want the proxied root", info, err)
	}

	// httpfs is asserted mounted rather than stat'd: describing a path on an
	// origin is a request, and nothing here runs on a machine with a network.
	// What the composition root is answerable for is that the mount is in the
	// table and says what it is.
	if caps := table.Capabilities("/origin"); !caps.ReadOnly || !caps.ObjectStore {
		t.Errorf("Capabilities(/origin) = %+v, want a read-only object store", caps)
	}
}

// serverinfofs reports the version this binary was built at, which is what
// makes it worth reading: a config that could name its own version could say a
// build was one it is not.
func TestServerinfofsReportsTheBuildVersion(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "v9.9.9", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/serverinfo", Kind: "serverinfofs"}})
	if err != nil {
		t.Fatalf("Table(/serverinfo) = %v", err)
	}

	content, err := table.Read(context.Background(), "/serverinfo/version")
	if err != nil {
		t.Fatalf("Read(/serverinfo/version) = %v", err)
	}
	if string(content) != "v9.9.9\n" {
		t.Errorf("Read(/serverinfo/version) = %q, want the version the build was given", content)
	}
}

// The end of the accounting: a call goes through the table to one mount, and
// the count of it is read back through the table from another. Nothing here
// exports anything, which is the point — the counters are readable on a laptop
// with no collector running.
func TestTrafficCountsWhatWentThroughTheTable(t *testing.T) {
	traffic := vfs.NewTraffic(time.Now)
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	registry.Record(traffic)
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), traffic, stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/scratch", Kind: "memfs"},
		{Prefix: "/serverinfo", Kind: "serverinfofs"},
	})
	if err != nil {
		t.Fatalf("Table() = %v", err)
	}

	if err := table.Write(context.Background(), "/scratch/note", []byte("text")); err != nil {
		t.Fatalf("Write(/scratch/note) = %v", err)
	}
	if _, err := table.Read(context.Background(), "/scratch/missing"); err == nil {
		t.Fatal("Read(/scratch/missing) succeeded, want a failure to count")
	}

	content, err := table.Read(context.Background(), "/serverinfo/traffic")
	if err != nil {
		t.Fatalf("Read(/serverinfo/traffic) = %v", err)
	}

	// The read of the tally is itself a call on the serverinfo mount and counts
	// as one, which is why that row reads 1.
	want := "mount\tkind\tcalls\tfailed\n" +
		"/scratch\tmemfs\t2\t1\n" +
		"/serverinfo\tserverinfofs\t1\t0\n"
	if string(content) != want {
		t.Errorf("Read(/serverinfo/traffic) = %q, want %q", content, want)
	}
}

// A proxy reaches its target through the same table of kinds as any other
// mount, which is what makes "a proxy over X" a line of config rather than a
// plugin per X.
func TestProxyfsForwardsToAKindTheConfigNames(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "public"), 0o755); err != nil {
		t.Fatalf("making the subtree = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "public", "notes"), []byte("text"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}

	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	spec := vfs.MountSpec{Prefix: "/alias", Kind: "proxyfs", Settings: map[string]string{
		"target_kind":   "localfs",
		"target_prefix": "/public",
		"root":          root,
	}}
	table, err := registry.Table([]vfs.MountSpec{spec})
	if err != nil {
		t.Fatalf("Table(/alias) = %v", err)
	}

	content, err := table.Read(context.Background(), "/alias/notes")
	if err != nil {
		t.Fatalf("Read(/alias/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/alias/notes) = %q, want %q", content, "text")
	}
}

// Only the settings a proxy reads for itself are its to declare. Everything
// else belongs to the kind behind it, and a proxy cannot say on that kind's
// behalf which of them is a credential, so they read back redacted.
func TestProxyfsDeclaresOnlyItsOwnSettings(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	want := []string{"target_kind", "target_prefix"}
	if public := registry.Public("proxyfs"); !slices.Equal(public, want) {
		t.Errorf("Public(proxyfs) = %v, want %v", public, want)
	}
}

// streamfs is the kind a log or a transcript is mounted as, and what makes it
// worth mounting is reached through the dispatcher: a caller holding the table
// follows a stream through AsStreamer, and a write it made through the table is
// what the follower receives.
func TestRegisterKindsMakesStreamfsMountable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/streams", Kind: streamfs.Kind, Settings: map[string]string{"follow": "0s"}},
	})
	if err != nil {
		t.Fatalf("Table(/streams) = %v", err)
	}

	ctx := context.Background()
	if err := table.Write(ctx, "/streams/log", []byte("first ")); err != nil {
		t.Fatalf("Write(/streams/log) = %v", err)
	}
	if err := table.Write(ctx, "/streams/log", []byte("second")); err != nil {
		t.Fatalf("Write(/streams/log) = %v", err)
	}

	stream := table.AsStreamer()
	if stream == nil {
		t.Fatal("AsStreamer() through the table = nil, want the dispatcher")
	}
	reader, err := stream.OpenRead(ctx, "/streams/log")
	if err != nil {
		t.Fatalf("OpenRead(/streams/log) = %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(/streams/log) = %v", err)
	}
	if string(content) != "first second" {
		t.Errorf("ReadAll(/streams/log) = %q, want %q", content, "first second")
	}
}

// How much of a stream is kept and how long a reader waits are what an operator
// reads back, and neither is a credential.
func TestStreamfsDeclaresItsCapacityAndFollowPublic(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	if public := registry.Public(streamfs.Kind); !slices.Equal(public, streamfs.Public) {
		t.Errorf("Public(streamfs) = %v, want %v", public, streamfs.Public)
	}
}

// streamrotatefs is the kind a log that has to be kept but must not grow is
// mounted as, and what makes it worth mounting is what a listing shows: a
// caller holding the table sees the segments a log sealed rather than being
// told what the mount discarded.
func TestRegisterKindsMakesStreamrotatefsMountable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{
		{Prefix: "/logs", Kind: streamrotatefs.Kind, Settings: map[string]string{"size": "4", "keep": "1"}},
	})
	if err != nil {
		t.Fatalf("Table(/logs) = %v", err)
	}

	ctx := context.Background()
	for _, written := range []string{"aaaa", "bbbb"} {
		if err := table.Write(ctx, "/logs/app", []byte(written)); err != nil {
			t.Fatalf("Write(/logs/app) = %v", err)
		}
	}

	entries, err := table.ReadDir(ctx, "/logs")
	if err != nil {
		t.Fatalf("ReadDir(/logs) = %v", err)
	}
	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	if want := []string{"app", "app.2"}; !slices.Equal(listed, want) {
		t.Errorf("ReadDir(/logs) after two rotations keeping one = %v, want %v", listed, want)
	}

	content, err := table.Read(ctx, "/logs/app.2")
	if err != nil {
		t.Fatalf("Read(/logs/app.2) = %v", err)
	}
	if string(content) != "bbbb" {
		t.Errorf("Read(/logs/app.2) = %q, want %q", content, "bbbb")
	}
}

// How large a segment grows and how many are kept are what an operator reads
// back, and neither is a credential.
func TestStreamrotatefsDeclaresItsSizeAndKeepPublic(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	if public := registry.Public(streamrotatefs.Kind); !slices.Equal(public, streamrotatefs.Public) {
		t.Errorf("Public(streamrotatefs) = %v, want %v", public, streamrotatefs.Public)
	}
}

// queuefs reaches two things an operator names, and both URLs carry a password.
// Declaring only the queue, its exchange and its batch is what makes them
// redacted everywhere the effective config is read back.
func TestQueuefsDeclaresOnlyWhatIsNotACredential(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	want := []string{"name", "exchange", "batch"}
	if public := registry.Public(queuefs.Kind); !slices.Equal(public, want) {
		t.Errorf("Public(queuefs) = %v, want %v", public, want)
	}
	for _, credential := range []string{"journal_url", "broker_url"} {
		if slices.Contains(registry.Public(queuefs.Kind), credential) {
			t.Errorf("Public(queuefs) names %q, want it redacted", credential)
		}
	}
}

// A queue with no journal named is a boot failure, and it is one before
// anything is dialled.
func TestQueuefsMountRefusesSettingsItCannotHonour(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	_, err := registry.Table([]vfs.MountSpec{{Prefix: "/queue", Kind: queuefs.Kind, Settings: map[string]string{"name": "orders"}}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a queuefs mount with no journal = %v, want fs.ErrInvalid", err)
	}
}

// vectorfs reaches a database an operator names, and its URL carries a
// password. Declaring only the index, its dimension and how many documents a
// question is answered with is what makes the URL redacted everywhere the
// effective config is read back.
func TestVectorfsDeclaresOnlyWhatIsNotACredential(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	want := []string{"name", "dimension", "neighbours"}
	if public := registry.Public(vectorfs.Kind); !slices.Equal(public, want) {
		t.Errorf("Public(vectorfs) = %v, want %v", public, want)
	}
	if slices.Contains(registry.Public(vectorfs.Kind), "index_url") {
		t.Error("Public(vectorfs) names index_url, want it redacted")
	}
}

// An index with no dimension named is a boot failure, and it is one before
// anything is dialled: the column a document's vector goes in is declared from
// that number and nothing can infer it.
func TestVectorfsMountRefusesSettingsItCannotHonour(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	_, err := registry.Table([]vfs.MountSpec{{Prefix: "/index", Kind: vectorfs.Kind, Settings: map[string]string{"name": "memories"}}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a vectorfs mount with no dimension = %v, want fs.ErrInvalid", err)
	}
}

// A build whose chain embeds nothing has no index to offer, and the refusal
// lands where an operator is still watching rather than on the first document.
func TestVectorfsMountRefusesAChainThatEmbedsNothing(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), nil, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	_, err := registry.Table([]vfs.MountSpec{{
		Prefix:   "/index",
		Kind:     vectorfs.Kind,
		Settings: map[string]string{"name": "memories", "index_url": "postgres://nowhere", "dimension": "8"},
	}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a vectorfs mount with no embedding endpoint = %v, want fs.ErrInvalid", err)
	}
}

// A build that names no corpus has nowhere to keep a document, and the refusal
// lands beside the one for a chain that embeds nothing: both are a mount that
// could store nothing and answer nothing.
func TestVectorfsMountRefusesABuildWithNoCorpus(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, nil, stubExtractor{}, nil, nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	_, err := registry.Table([]vfs.MountSpec{{
		Prefix:   "/index",
		Kind:     vectorfs.Kind,
		Settings: map[string]string{"name": "memories", "index_url": "postgres://nowhere", "dimension": "8"},
	}})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a vectorfs mount with no corpus = %v, want fs.ErrInvalid", err)
	}
}

// testCorpus is a corpus in a directory of this test's own. Nothing in this file
// stores a document: what the composition root is answerable for is that the
// kind is there and is given somewhere to put one.
func testCorpus(t *testing.T) vectorfs.DocStore {
	t.Helper()

	docs, err := corpus("file://" + t.TempDir())
	if err != nil {
		t.Fatalf("corpus() = %v", err)
	}
	return docs
}

// stubEmbedder stands where the gateway's routed Embedder does. Nothing in this
// file embeds anything: what the composition root is answerable for is that the
// kind is there and takes what it says it takes.
type stubEmbedder struct{}

func (stubEmbedder) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }

// testGraphs is the store a graphfs mount keeps its blob in, in a corpus of
// this test's own.
func testGraphs(t *testing.T) graphKeeper {
	t.Helper()

	return graphStore(testCorpus(t))
}

// stubExtractor stands where the gateway's routed EntityExtractor does. Nothing
// in this file reads a text, for the reason nothing in it embeds one.
type stubExtractor struct{}

func (stubExtractor) ExtractEntities(context.Context, string) (llm.Extraction, error) {
	return llm.Extraction{}, nil
}

// An export mount over a kind the config names is the whole path this ticket
// is about: a derived name is served through the table, and a change to what
// it was derived from is not something a caller has to know to ask about.
func TestExportfsRefreshesAfterAnInputOnDiskChanges(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "notes")
	if err := os.WriteFile(input, []byte("first"), 0o644); err != nil {
		t.Fatalf("writing the host file = %v", err)
	}

	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	spec := vfs.MountSpec{Prefix: "/exports", Kind: "exportfs", Settings: map[string]string{
		"source_kind": "localfs",
		"exports":     "report=/notes",
		"root":        root,
	}}
	table, err := registry.Table([]vfs.MountSpec{spec})
	if err != nil {
		t.Fatalf("Table(/exports) = %v", err)
	}

	ctx := context.Background()
	content, err := table.Read(ctx, "/exports/report")
	if err != nil {
		t.Fatalf("Read(/exports/report) = %v", err)
	}
	if string(content) != "first" {
		t.Fatalf("Read(/exports/report) = %q, want %q", content, "first")
	}

	if err := os.WriteFile(input, []byte("second time"), 0o644); err != nil {
		t.Fatalf("rewriting the host file = %v", err)
	}

	content, err = table.Read(ctx, "/exports/report")
	if err != nil {
		t.Fatalf("Read(/exports/report) = %v", err)
	}
	if string(content) != "second time" {
		t.Errorf("Read(/exports/report) after the input changed = %q, want %q", content, "second time")
	}
}

// Which paths an export is stale against is the first thing anyone asks when it
// looks wrong, so both settings the export mount reads for itself are declared.
// The source kind's settings are its own and read back redacted.
func TestExportfsDeclaresOnlyItsOwnSettings(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerKinds(registry, "test", kvfs.NewMemStore(), vfs.NewTraffic(time.Now), stubEmbedder{}, testCorpus(t), stubExtractor{}, testGraphs(t), nil); err != nil {
		t.Fatalf("registerKinds(, nil) = %v", err)
	}

	want := []string{"source_kind", "exports"}
	if public := registry.Public("exportfs"); !slices.Equal(public, want) {
		t.Errorf("Public(exportfs) = %v, want %v", public, want)
	}
}
