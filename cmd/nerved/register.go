package main

import (
	"errors"

	"github.com/tunedev/nerve/internal/adapters/outbound/devfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/exportfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/heartbeatfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/hellofs"
	"github.com/tunedev/nerve/internal/adapters/outbound/httpfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/proxyfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/serverinfofs"
	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/streamrotatefs"
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
// namespace and sqlfs its database file, for the same reason; objectfs names
// its endpoint, its bucket and its region, and by saying nothing about the two
// credentials beside them makes them read back as redacted.
//
// kvfs is given the one keyspace this build has, built here, because every kvfs
// mount is a namespaced part of a store rather than a store of its own. That is
// what makes the namespace mean anything, and the composition root is where a
// thing two mounts share is owned.
//
// serverinfofs is given the version this binary was built at, for the reason
// kvfs is given the store: a mount that let a config name its own version could
// lie about which build an agent is talking to.
//
// streamfs and streamrotatefs each name how much of a log they hold: the first
// its ring capacity and how long a reader follows, the second its segment size
// and how many segments it keeps. Neither holds a credential, so both name
// everything they take.
//
// queuefs names its queue, its exchange and its batch, and by saying nothing
// about the two URLs beside them makes the journal's and the broker's
// credentials read back as redacted.
//
// proxyfs is given the registry's bare build, so the kind behind a proxy comes
// out of the same table of kinds as any other mount and is wrapped exactly
// once — by the Build that puts the proxy itself into the table. exportfs is
// given it for the same reason: the kind holding an export's inputs is a kind
// like any other, and the export mount over it is what goes into the table.
func registerKinds(registry *vfs.Registry, version string) error {
	return errors.Join(
		registry.Register(memfs.Kind, memfs.Factory),
		registry.Register(localfs.Kind, localfs.Factory, localfs.RootSetting),
		registry.Register(kvfs.Kind, kvfs.Factory(kvfs.NewMemStore()), kvfs.NamespaceSetting),
		registry.Register(objectfs.Kind, objectfs.Factory, objectfs.Public...),
		registry.Register(queuefs.Kind, queuefs.Factory, queuefs.Public...),
		registry.Register(sqlfs.Kind, sqlfs.Factory, sqlfs.PathSetting),
		registry.Register(streamfs.Kind, streamfs.Factory, streamfs.Public...),
		registry.Register(streamrotatefs.Kind, streamrotatefs.Factory, streamrotatefs.Public...),
		registry.Register(hellofs.Kind, hellofs.Factory, hellofs.MessageSetting),
		registry.Register(heartbeatfs.Kind, heartbeatfs.Factory, heartbeatfs.IntervalSetting),
		registry.Register(serverinfofs.Kind, serverinfofs.Factory(version)),
		registry.Register(devfs.Kind, devfs.Factory, devfs.SizeSetting),
		registry.Register(httpfs.Kind, httpfs.Factory, httpfs.Public...),
		registry.Register(proxyfs.Kind, proxyfs.Factory(registry.Plugin), proxyfs.Public...),
		registry.Register(exportfs.Kind, exportfs.Factory(registry.Plugin), exportfs.Public...),
	)
}
