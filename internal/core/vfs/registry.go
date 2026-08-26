package vfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// Factory builds one plugin from the settings its config names.
//
// Settings are strings because a config file, an environment variable and a
// command line all carry strings. What a setting means is the plugin's
// business, parsed where the plugin is built and never here.
type Factory func(settings map[string]string) (FileSystem, error)

// MountSpec is one line of mount config: where a plugin goes in the tree, which
// kind it is, and what that kind needs to know.
type MountSpec struct {
	// Prefix is the subtree the plugin answers for, rooted and clean.
	Prefix string
	// Kind is the registered name of the factory that builds the plugin.
	Kind string
	// Settings are what that factory needs. A kind that needs nothing takes a
	// nil map.
	Settings map[string]string
}

// Registry maps a kind name onto the factory that builds it, so a new mount is
// a line of config naming a registered kind rather than an edit to a switch or
// a factory function.
//
// Nothing here knows a concrete plugin. Kinds are registered at composition,
// which is the only place that does, and a registry is written there and read
// afterwards rather than shared across goroutines.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns a registry holding no kinds. Every kind is unknown until
// something registers it.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register puts factory under kind. It fails with fs.ErrExist on a kind already
// taken, because two factories under one name make which plugin a config gets
// depend on registration order.
func (r *Registry) Register(kind string, factory Factory) error {
	if _, taken := r.factories[kind]; taken {
		return fmt.Errorf("register kind %q: %w", kind, fs.ErrExist)
	}

	r.factories[kind] = factory
	return nil
}

// Build makes the plugin one spec names. A kind no one registered fails with
// fs.ErrNotExist naming what is registered, because a misspelled kind is the
// boot failure this ticket exists to make legible.
func (r *Registry) Build(spec MountSpec) (FileSystem, error) {
	factory, known := r.factories[spec.Kind]
	if !known {
		return nil, fmt.Errorf("mount %q: kind %q is not registered; this build has %s: %w", spec.Prefix, spec.Kind, r.known(), fs.ErrNotExist)
	}

	fsys, err := factory(spec.Settings)
	if err != nil {
		return nil, fmt.Errorf("mount %q of kind %q: %w", spec.Prefix, spec.Kind, err)
	}
	return fsys, nil
}

// Table builds every spec and mounts it, which is how a config document becomes
// the tree a caller holds. It stops at the first spec it cannot honour, so a
// process never boots on half a mount table.
func (r *Registry) Table(specs []MountSpec) (*MountTable, error) {
	table := NewMountTable()

	for _, spec := range specs {
		fsys, err := r.Build(spec)
		if err != nil {
			return nil, err
		}
		if err := table.Mount(spec.Prefix, fsys); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// known lists the registered kinds for an error message, so a misspelled kind
// at boot shows an operator what the build actually has.
func (r *Registry) known() string {
	kinds := slices.Sorted(maps.Keys(r.factories))
	if len(kinds) == 0 {
		return "no kinds"
	}
	return strings.Join(kinds, ", ")
}
