package exportfs_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/exportfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// built stands in for the registry's bare build, handing back one source and
// recording the spec it was asked for, which is what a mount config becomes.
type built struct {
	source vfs.FileSystem
	asked  vfs.MountSpec
}

func (b *built) build(spec vfs.MountSpec) (vfs.FileSystem, error) {
	b.asked = spec
	if spec.Kind != "memfs" {
		return nil, fmt.Errorf("kind %q is not registered: %w", spec.Kind, fs.ErrNotExist)
	}
	return b.source, nil
}

func factory(t *testing.T, content map[string]string) (vfs.Factory, *built) {
	t.Helper()

	registry := &built{source: source(t, content)}
	return exportfs.Factory(registry.build), registry
}

func TestFactoryBuildsTheExportsAMountDeclares(t *testing.T) {
	build, _ := factory(t, map[string]string{"/a": "one", "/b": "two"})

	fsys, err := build(map[string]string{
		exportfs.SourceKindSetting: "memfs",
		exportfs.ExportsSetting:    "report=/a,/b;first=/a",
	})
	if err != nil {
		t.Fatalf("Factory() = %v", err)
	}

	if held := read(t, fsys, "/report"); held != "onetwo" {
		t.Errorf("Read(/report) = %q, want %q", held, "onetwo")
	}
	if held := read(t, fsys, "/first"); held != "one" {
		t.Errorf("Read(/first) = %q, want %q", held, "one")
	}
}

// The declarations an operator wrote are read back with their spacing gone, so
// a config laid out to be read still names the paths it means.
func TestFactoryReadsDeclarationsAnOperatorSpacedOut(t *testing.T) {
	build, _ := factory(t, map[string]string{"/a": "one", "/b": "two"})

	fsys, err := build(map[string]string{
		exportfs.SourceKindSetting: "memfs",
		exportfs.ExportsSetting:    " report = /a , /b ; ",
	})
	if err != nil {
		t.Fatalf("Factory() = %v", err)
	}
	if held := read(t, fsys, "/report"); held != "onetwo" {
		t.Errorf("Read(/report) = %q, want %q", held, "onetwo")
	}
}

// Everything the export mount does not take is the source kind's, and reaches
// it unchanged: that is what makes an export over any kind a line of config.
func TestFactoryPassesTheRestThroughToTheSourceKind(t *testing.T) {
	build, registry := factory(t, map[string]string{"/a": "one"})

	if _, err := build(map[string]string{
		exportfs.SourceKindSetting: "memfs",
		exportfs.ExportsSetting:    "report=/a",
		"namespace":                "agents",
	}); err != nil {
		t.Fatalf("Factory() = %v", err)
	}

	if registry.asked.Kind != "memfs" {
		t.Errorf("the source was built as kind %q, want %q", registry.asked.Kind, "memfs")
	}
	if got := registry.asked.Settings["namespace"]; got != "agents" {
		t.Errorf("the source was given namespace %q, want %q", got, "agents")
	}
	for _, taken := range []string{exportfs.SourceKindSetting, exportfs.ExportsSetting} {
		if _, passed := registry.asked.Settings[taken]; passed {
			t.Errorf("the source was given %q, which is the export mount's own setting", taken)
		}
	}
}

func TestFactoryRefusesAMountItCannotBuild(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings map[string]string
		want     error
	}{
		{
			"no source kind",
			map[string]string{exportfs.ExportsSetting: "report=/a"},
			fs.ErrInvalid,
		},
		{
			"an export mount behind an export mount",
			map[string]string{exportfs.SourceKindSetting: exportfs.Kind, exportfs.ExportsSetting: "report=/a"},
			fs.ErrInvalid,
		},
		{
			"no exports declared",
			map[string]string{exportfs.SourceKindSetting: "memfs"},
			fs.ErrInvalid,
		},
		{
			"a declaration naming no inputs",
			map[string]string{exportfs.SourceKindSetting: "memfs", exportfs.ExportsSetting: "report"},
			fs.ErrInvalid,
		},
		{
			"a declaration with an empty input",
			map[string]string{exportfs.SourceKindSetting: "memfs", exportfs.ExportsSetting: "report=/a,,/b"},
			fs.ErrInvalid,
		},
		{
			"the same export twice",
			map[string]string{exportfs.SourceKindSetting: "memfs", exportfs.ExportsSetting: "report=/a;report=/b"},
			fs.ErrExist,
		},
		{
			"a source kind this build does not have",
			map[string]string{exportfs.SourceKindSetting: "nosuchfs", exportfs.ExportsSetting: "report=/a"},
			fs.ErrNotExist,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build, _ := factory(t, map[string]string{"/a": "one"})

			if _, err := build(tc.settings); !errors.Is(err, tc.want) {
				t.Errorf("Factory(%v) = %v, want %v", tc.settings, err, tc.want)
			}
		})
	}
}

// The source is built at the root, because the paths an export names are the
// ones an operator wrote in the declaration.
func TestFactoryBuildsTheSourceAtTheRoot(t *testing.T) {
	build, registry := factory(t, map[string]string{"/a": "one"})

	if _, err := build(map[string]string{
		exportfs.SourceKindSetting: "memfs",
		exportfs.ExportsSetting:    "report=/a",
	}); err != nil {
		t.Fatalf("Factory() = %v", err)
	}
	if registry.asked.Prefix != "/" {
		t.Errorf("the source was built at %q, want %q", registry.asked.Prefix, "/")
	}
}

// A build wires a materialisation the registry has no name for, which is the
// half of this plugin a config cannot reach.
func TestNewTakesAMaterialisationAConfigCannotName(t *testing.T) {
	src := memfs.New()
	if err := src.Write(t.Context(), "/a", []byte("one")); err != nil {
		t.Fatalf("Write(/a) = %v", err)
	}

	fsys, err := exportfs.New(src, []exportfs.Export{{
		Name:   "counted",
		Inputs: []string{"/a"},
		From: func(ctx context.Context, source vfs.FileSystem, inputs []string) ([]byte, error) {
			content, err := source.Read(ctx, inputs[0])
			if err != nil {
				return nil, err
			}
			return fmt.Appendf(nil, "%d\n", len(content)), nil
		},
	}})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	if held := read(t, fsys, "/counted"); held != "3\n" {
		t.Errorf("Read(/counted) = %q, want %q", held, "3\n")
	}
}
