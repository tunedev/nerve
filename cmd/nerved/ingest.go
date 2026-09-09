package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// group is the consumer this process joins on the queue's exchange. Every
// nerved running it competes for the same notifications, which is what makes a
// second replica share the work rather than repeat it.
const group = "ingest"

// uploads is the tree the resource store writes through: the mount table with
// every write reported to the queue, or the table itself when no queue is
// named.
//
// The observer goes over the table rather than under it, so the path an ingest
// worker is told about is the path it can read back. The emitter publishes
// through the plain table: a job is a write like any other, and reporting it
// would be the queue telling itself about its own messages.
//
// A build with no ingest queue still takes uploads. What it does not do is
// derive anything from them, and that is one line of config rather than a
// different composition.
func uploads(table vfs.FileSystem, queue string) vfs.FileSystem {
	if queue == "" {
		return table
	}
	return vfs.WithWriteObserver(table, queuefs.NewEmitter(table, queue))
}

// indexingLedger builds the record of where the ingestion of every document
// stands, under root on the tree the mounts present. A build that names no root
// holds none, which is a build that ingests nothing: config refuses a queue
// without one.
func indexingLedger(fsys vfs.FileSystem, root string) (*ingest.Ledger, error) {
	if root == "" {
		return nil, nil
	}
	return ingest.NewLedger(fsys, root, time.Now)
}

// ingesting is the consuming end of ingestion this process runs: the worker
// that takes jobs off the queue, the listener holding the subscription that
// drives it, and the goroutine the two run on.
//
// A build that ingests nothing holds the zero value, and every method here is
// something it does not do. The alternative is a branch around the goroutine at
// both ends of main.
type ingesting struct {
	worker   *queuefs.Worker
	listener *queuefs.Consumer
	stopped  chan error
}

// consumer builds what this process runs on the far end of the queue: the
// pipeline that ingests one resource, and the worker that hands it the jobs the
// resource store's own writes queued.
//
// Nothing is built when no ingest queue is named. Everything the pipeline needs
// beyond that is required by config, so a queue named without a store to ingest
// from fails at boot rather than by claiming jobs it can resolve nothing from.
//
// The queue mount is checked for being a queue before anything subscribes to
// it. A job key is a whole changed path, so a mount that is not a flat key
// space refuses the deep key it is given — and an observer's report is dropped
// by design, which would leave a process queueing nothing, saying nothing and
// looking healthy.
func consumer(table vfs.FileSystem, mounts []vfs.MountSpec, queue, resourcesRoot string,
	resources *resource.Store, ledger *ingest.Ledger, processors *ingest.Registry) (*ingesting, error) {
	if queue == "" {
		return &ingesting{}, nil
	}

	jobs := path.Join(queue, queuefs.Jobs)
	switch caps := table.Capabilities(jobs); {
	case !caps.ObjectStore:
		return nil, fmt.Errorf("ingest-queue %q: %s is not a flat key space, and a job key is a whole path: "+
			"the jobs would be refused, the report dropped and nothing said: %w", queue, jobs, fs.ErrInvalid)
	case !caps.ReadDestructive:
		return nil, fmt.Errorf("ingest-queue %q: reading %s does not consume, so nothing settles which "+
			"worker claims a job: %w", queue, jobs, fs.ErrInvalid)
	}

	spec, err := mountedAt(mounts, queue)
	if err != nil {
		return nil, err
	}
	listener, err := queuefs.Consuming(spec.Settings, group, subtree(resourcesRoot))
	if err != nil {
		return nil, fmt.Errorf("ingest-queue %q: %w", queue, err)
	}

	pipeline := ingest.New(resources, ledger, ingest.Extraction(resources, processors))
	handle := func(ctx context.Context, key string, _ []byte) error { return pipeline.Ingest(ctx, key) }
	return &ingesting{worker: queuefs.NewWorker(table, queue, listener, handle), listener: listener}, nil
}

// mountedAt is the mount config the queue prefix names. The listener and the
// mount reach the same broker, so what the listener reaches it with is what the
// mount was configured with rather than a second pair of settings to keep in
// step.
//
// A prefix that is not a mount is refused. It could only be a path inside one,
// and a queue is a mount.
func mountedAt(mounts []vfs.MountSpec, prefix string) (vfs.MountSpec, error) {
	for _, spec := range mounts {
		if spec.Prefix == prefix {
			return spec, nil
		}
	}
	return vfs.MountSpec{}, fmt.Errorf("ingest-queue %q names no mount: %w", prefix, fs.ErrNotExist)
}

// subtree is the pattern a worker subscribes with: everything under root. A job
// key is the changed path with its leading separator gone, so what the worker
// hears about is the tree's own vocabulary and never a routing key.
func subtree(root string) string {
	return strings.TrimPrefix(root, "/") + "/#"
}

// start runs the worker until ctx is done. The goroutine ends when Listen
// returns, which cancelling makes it do; the channel is buffered so it cannot
// outlive stop blocked on a send.
func (i *ingesting) start(ctx context.Context) {
	if i.worker == nil {
		return
	}
	i.stopped = make(chan error, 1)
	go func() { i.stopped <- i.worker.Run(ctx) }()
}

// stop waits for the worker to finish and lets the subscription go.
//
// It is called after the server has stopped answering, for the reason the
// mounts are released there: a worker mid-job is holding a mount, and the
// connection it listens on is the last thing to close.
//
// A listener nothing was ever started on is still closed. It reached the broker
// where it was built, so a process that gave up between composing and running
// gives the connection back rather than leaving it open on the server.
func (i *ingesting) stop() error {
	if i.listener == nil {
		return nil
	}
	if i.stopped == nil {
		return i.listener.Close()
	}
	return errors.Join(<-i.stopped, i.listener.Close())
}

// describe says what this process ingests, or that it ingests nothing. Which of
// the two it is is the first thing an operator wondering why an upload never
// became searchable needs to know.
func (i *ingesting) describe(queue, ledger string) {
	if i.worker == nil {
		slog.Info("pipeline", "queue", "none", "note", "uploads are taken and nothing is derived from them")
		return
	}
	slog.Info("pipeline", "queue", queue, "group", group, "ledger-root", ledger)
}
