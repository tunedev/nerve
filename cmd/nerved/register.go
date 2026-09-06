package main

import (
	"errors"

	"github.com/tunedev/nerve/internal/adapters/outbound/cuefact"
	"github.com/tunedev/nerve/internal/adapters/outbound/devfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/exportfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/heartbeatfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/hellofs"
	"github.com/tunedev/nerve/internal/adapters/outbound/httpfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/adapters/outbound/objectfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
	"github.com/tunedev/nerve/internal/adapters/outbound/pdfproc"
	"github.com/tunedev/nerve/internal/adapters/outbound/proxyfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/serverinfofs"
	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/streamrotatefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/adapters/outbound/textproc"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/llm"
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
// kvfs is given the one keyspace this build has, because every kvfs mount is a
// namespaced part of a store rather than a store of its own. That is what makes
// the namespace mean anything, and the composition root is where a thing two
// mounts share is owned — which is also why the keyspace arrives here already
// built: whether it is a map or a bucket is what config says, and this table
// does not read config.
//
// serverinfofs is given the version this binary was built at and the tally the
// registry counts through, for the reason kvfs is given the store: a mount that
// let a config name its own version could lie about which build an agent is
// talking to, and the counters it reads back are the process's own.
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
// vectorfs is given the chain's routed Embedder and the corpus, because a
// document is stored with the vector made from it and nothing but the
// composition root knows which endpoints make one or where the documents go. A
// build whose chain embeds nothing, or that names no corpus, hands over a nil,
// and the kind refuses a mount rather than booting an index that could answer
// nothing. The corpus arrives here already built for the reason the keyspace
// does: whether it is a directory or a bucket is what config says, and this
// table does not read config. It names its index, its dimension and how many
// documents a question is answered with, and by saying nothing about the URL
// beside them makes the database's credentials read back as redacted.
//
// proxyfs is given the registry's bare build, so the kind behind a proxy comes
// out of the same table of kinds as any other mount and is wrapped exactly
// once — by the Build that puts the proxy itself into the table. exportfs is
// given it for the same reason: the kind holding an export's inputs is a kind
// like any other, and the export mount over it is what goes into the table.
func registerKinds(registry *vfs.Registry, version string, keyspace kvfs.Store, traffic *vfs.Traffic, embed llm.Embedder, corpus vectorfs.DocStore) error {
	return errors.Join(
		registry.Register(memfs.Kind, memfs.Factory),
		registry.Register(localfs.Kind, localfs.Factory, localfs.RootSetting),
		registry.Register(kvfs.Kind, kvfs.Factory(keyspace), kvfs.NamespaceSetting),
		registry.Register(objectfs.Kind, objectfs.Factory, objectfs.Public...),
		registry.Register(queuefs.Kind, queuefs.Factory, queuefs.Public...),
		registry.Register(sqlfs.Kind, sqlfs.Factory, sqlfs.PathSetting),
		registry.Register(vectorfs.Kind, vectorfs.Factory(embed, corpus), vectorfs.Public...),
		registry.Register(streamfs.Kind, streamfs.Factory, streamfs.Public...),
		registry.Register(streamrotatefs.Kind, streamrotatefs.Factory, streamrotatefs.Public...),
		registry.Register(hellofs.Kind, hellofs.Factory, hellofs.MessageSetting),
		registry.Register(heartbeatfs.Kind, heartbeatfs.Factory, heartbeatfs.IntervalSetting),
		registry.Register(serverinfofs.Kind, serverinfofs.Factory(version, traffic)),
		registry.Register(devfs.Kind, devfs.Factory, devfs.SizeSetting),
		registry.Register(httpfs.Kind, httpfs.Factory, httpfs.Public...),
		registry.Register(proxyfs.Kind, proxyfs.Factory(registry.Plugin), proxyfs.Public...),
		registry.Register(exportfs.Kind, exportfs.Factory(registry.Plugin), exportfs.Public...),
	)
}

// registerProcessors puts every format this build can read under the media type
// an upload declares it as. It is here for the reason registerKinds is: the
// composition root is the only place that knows a concrete processor, and
// adding a format is a package plus a line here rather than an edit to
// whatever dispatches.
//
// A processor is registered under every type it reads. textproc takes the two
// that are already text; pdfproc takes the one it parses.
func registerProcessors(registry *ingest.Registry) error {
	return errors.Join(
		registry.Register(textproc.New(), textproc.MediaTypes...),
		registry.Register(pdfproc.New(), pdfproc.MediaType),
	)
}

// registerProviders puts every model endpoint kind this build has under the
// name a provider config asks for it by. It is here for the reason
// registerKinds is: the composition root is the only place that knows a
// concrete provider, and a second endpoint is a package plus a line here rather
// than an edit to whatever routes.
//
// termsum and nounlink take no settings, so they name none. ollama names its
// endpoint and its model, and cuefact names whose turns it reads, because that
// is an operator's business and not a credential. A kind that reached a hosted
// model would say nothing about the key beside them, which is what makes the
// key read back as redacted.
//
// The four serve different capabilities, so which one a call reaches is the
// accessor it asked for rather than the order they are listed in here. A chain
// naming all four summarises through termsum, embeds through ollama, remembers
// through cuefact and reads entities through nounlink.
func registerProviders(registry *llm.Registry) error {
	return errors.Join(
		registry.Register(termsum.Kind, termsum.Factory),
		registry.Register(ollama.Kind, ollama.Factory, ollama.Public...),
		registry.Register(cuefact.Kind, cuefact.Factory, cuefact.RoleSetting),
		registry.Register(nounlink.Kind, nounlink.Factory),
	)
}
