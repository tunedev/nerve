package exportfs

import (
	"fmt"
	"io/fs"
	"maps"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for materialised exports.
const Kind = "exportfs"

// The settings this kind takes. Everything else a mount names is passed through
// to the kind holding the inputs, the way a proxy passes its target's settings
// through, so an export over any kind is config rather than a new plugin.
const (
	SourceKindSetting = "source_kind"
	ExportsSetting    = "exports"
)

// Public names the settings this kind reads back to an operator: which kind
// holds the inputs, and what each export is derived from. Neither is a
// credential, and which paths an export is stale against is the first thing
// anyone asks when it looks wrong. The pass-through settings are the source
// kind's and read back redacted, because only the kind a registry was asked for
// gets to say which of its settings is a secret.
var Public = []string{SourceKindSetting, ExportsSetting}

// The exports setting is a list of declarations, each a name and the inputs it
// is derived from:
//
//	report=/logs/monday,/logs/tuesday;index=/documents/manifest
const (
	betweenExports = ";"
	beforeInputs   = "="
	betweenInputs  = ","
)

// Factory builds an export mount over a source it asks build for. build is the
// registry's bare build rather than its wrapping one, for the reason a proxy
// uses the same: the export mount is wrapped on the way into the table, and a
// source wrapped a second time would be retried by two layers.
//
// Every export a config declares is joined from its inputs, because a
// materialisation that does more than gather is code and a config names kinds
// rather than functions. A build with a query engine in it hands the function
// to New at composition instead.
//
// An export mount over an export mount is refused. The inner one's inputs are
// stamped against the source and the outer one's against the inner, so a change
// reaches the outer export only once the inner one has been read — a refresh
// that depends on somebody having asked, which is what this plugin exists to
// take away.
func Factory(build func(vfs.MountSpec) (vfs.FileSystem, error)) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		kind, named := settings[SourceKindSetting]
		if !named {
			return nil, fmt.Errorf("kind %q needs a %q setting naming the kind its inputs are held by: %w", Kind, SourceKindSetting, fs.ErrInvalid)
		}
		if kind == Kind {
			return nil, fmt.Errorf("kind %q over %q would refresh only once something read the inner one: %w", Kind, Kind, fs.ErrInvalid)
		}

		exports, err := parseExports(settings[ExportsSetting])
		if err != nil {
			return nil, err
		}

		passed := maps.Clone(settings)
		delete(passed, SourceKindSetting)
		delete(passed, ExportsSetting)

		source, err := build(vfs.MountSpec{Prefix: "/", Kind: kind, Settings: passed})
		if err != nil {
			return nil, fmt.Errorf("kind %q over %q: %w", Kind, kind, err)
		}

		mount, err := New(source, exports)
		if err != nil {
			return nil, fmt.Errorf("kind %q: %w", Kind, err)
		}
		return mount, nil
	}
}

// parseExports reads the declarations a config wrote. Each is a name, then the
// paths it is derived from. Whitespace around any of them is an operator's
// spacing rather than part of a name.
func parseExports(declared string) ([]Export, error) {
	if strings.TrimSpace(declared) == "" {
		return nil, fmt.Errorf("kind %q needs an %q setting, spelled %s: %w", Kind, ExportsSetting, "name=input,input;name=input", fs.ErrInvalid)
	}

	var exports []Export
	for _, one := range strings.Split(declared, betweenExports) {
		if strings.TrimSpace(one) == "" {
			continue
		}

		name, joined, found := strings.Cut(one, beforeInputs)
		if !found {
			return nil, fmt.Errorf("export %q names no inputs, spelled %s: %w", strings.TrimSpace(one), "name=input,input", fs.ErrInvalid)
		}

		inputs, err := parseInputs(joined)
		if err != nil {
			return nil, err
		}
		exports = append(exports, Export{Name: strings.TrimSpace(name), Inputs: inputs, From: Join})
	}
	return exports, nil
}

// parseInputs reads the paths one export is derived from, refusing an empty
// one: a blank between two separators is a typo, and an export stamped against
// a path nobody named would never go stale.
func parseInputs(joined string) ([]string, error) {
	var inputs []string

	for _, input := range strings.Split(joined, betweenInputs) {
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			return nil, fmt.Errorf("an export derived from %q names an empty input: %w", strings.TrimSpace(joined), fs.ErrInvalid)
		}
		inputs = append(inputs, trimmed)
	}
	return inputs, nil
}
