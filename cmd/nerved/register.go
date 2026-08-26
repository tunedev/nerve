package main

import (
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// registerKinds puts every storage kind this build has under the name a mount
// config uses to ask for it. It is here because the composition root is the
// only place that knows a concrete plugin: nothing in internal/core does, and
// adding a kind to a build is adding a line here rather than editing a switch.
//
// A kind names the settings it is willing to read back to an operator. memfs
// takes none, so it names none.
func registerKinds(registry *vfs.Registry) error {
	return registry.Register(memfs.Kind, memfs.Factory)
}
