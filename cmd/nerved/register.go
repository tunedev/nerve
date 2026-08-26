package main

import (
	"errors"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// registerKinds puts every storage kind this build has under the name a mount
// config uses to ask for it. It is here because the composition root is the
// only place that knows a concrete plugin: nothing in internal/core does, and
// adding a kind to a build is adding a line here rather than editing a switch.
//
// A kind names the settings it is willing to read back to an operator. memfs
// takes none, so it names none; localfs names its root, because which disk a
// mount is on is what an operator has to be able to read back; kvfs names its
// namespace, for the same reason; objectfs names its endpoint, its bucket and
// its region, and by saying nothing about the two credentials beside them makes
// them read back as redacted.
//
// kvfs is given the one keyspace this build has, built here, because every kvfs
// mount is a namespaced part of a store rather than a store of its own. That is
// what makes the namespace mean anything, and the composition root is where a
// thing two mounts share is owned.
func registerKinds(registry *vfs.Registry) error {
	return errors.Join(
		registry.Register(memfs.Kind, memfs.Factory),
		registry.Register(localfs.Kind, localfs.Factory, localfs.RootSetting),
		registry.Register(kvfs.Kind, kvfs.Factory(kvfs.NewMemStore()), kvfs.NamespaceSetting),
		registry.Register(objectfs.Kind, objectfs.Factory, objectfs.Public...),
	)
}
