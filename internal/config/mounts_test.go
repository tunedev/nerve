package config_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// named is a plugin that answers Read with the name its settings gave it, so a
// test can see which mount a path reached. It supports nothing optional, which
// is what NoAccessors is for.
type named struct {
	vfs.NoAccessors
	name string
}

func (n named) Read(context.Context, string) ([]byte, error)          { return []byte(n.name), nil }
func (named) Create(context.Context, string) error                    { return nil }
func (named) Mkdir(context.Context, string) error                     { return nil }
func (named) Remove(context.Context, string) error                    { return nil }
func (named) RemoveAll(context.Context, string) error                 { return nil }
func (named) Write(context.Context, string, []byte) error             { return nil }
func (named) ReadDir(context.Context, string) ([]vfs.FileInfo, error) { return nil, nil }
func (named) Stat(context.Context, string) (vfs.FileInfo, error)      { return vfs.FileInfo{}, nil }
func (named) Rename(context.Context, string, string) error            { return nil }
func (named) Chmod(context.Context, string, fs.FileMode) error        { return nil }
func (named) Capabilities(string) vfs.Caps                            { return vfs.Caps{} }

// The acceptance criterion end to end: a mount that exists only because a
// config document names it, reached through the table, with nothing in this
// test naming a concrete plugin.
func TestAMountAppearsFromAConfigFile(t *testing.T) {
	cfg, err := config.Load(config.Sources{File: writeFile(t, `{
		"mounts": [
			{"prefix": "/",      "kind": "named", "settings": {"name": "root"}},
			{"prefix": "/queue", "kind": "named", "settings": {"name": "queue"}}
		]
	}`)})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	registry := vfs.NewRegistry(cfg.CallTimeoutDuration(), cfg.RetryPolicy())
	if err := registry.Register("named", func(settings map[string]string) (vfs.FileSystem, error) {
		return named{name: settings["name"]}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	table, err := registry.Table(cfg.MountSpecs())
	if err != nil {
		t.Fatalf("build table: %v", err)
	}

	got, err := table.Read(context.Background(), "/queue/jobs/next")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "queue" {
		t.Errorf("read reached mount %q, want %q", got, "queue")
	}
}

func TestAConfigWithNoMountsResolvesNone(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if specs := cfg.MountSpecs(); len(specs) != 0 {
		t.Errorf("defaults resolved %d mounts, want none", len(specs))
	}
}

func TestAMisspelledMountKeyIsRefused(t *testing.T) {
	_, err := config.Load(config.Sources{File: writeFile(t, `{
		"mounts": [{"prefix": "/", "kind": "named", "setting": {"name": "root"}}]
	}`)})
	if err == nil {
		t.Fatal("a misspelled mount key was accepted")
	}
}
