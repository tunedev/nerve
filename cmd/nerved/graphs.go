package main

import (
	"context"
	"io/fs"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// graphPrefix is what a graph's blob is filed under in the corpus, so a graph
// and a vector index that happen to share a name do not share a key. It is a
// namespace and not a guarantee: the corpus is one keyspace, and an operator who
// names an index "graphs" gets what they asked for.
const graphPrefix = "graphs/"

// snapshotPrefix is what a graph's snapshots are filed under, which is not
// inside graphPrefix: a snapshot is a different thing from a graph and a graph
// named after a label is not one of its own snapshots. A label follows the
// graph's name, so one graph's snapshots list without reading another's.
const snapshotPrefix = "graph-snapshots/"

// graphMode is what a graph's blob is stored with. It is not a file anyone
// reaches by name, so there is nothing for a permission to say about it.
const graphMode = fs.FileMode(0o644)

// graphBlobs keeps graphs in the same corpus the vector indexes keep their
// documents in. It is here because the composition root is the only place that
// knows both a graph and the disk or the bucket under it, and because a store
// two mounts share is owned where the things two mounts share are owned.
//
// It is two methods over eight. graphfs asks for somewhere to put one blob and
// get it back, which is the least a directory and a bucket both do, so the
// plugin never sees the verbs a corpus has for listing and moving documents it
// has no documents to list.
type graphBlobs struct{ docs vectorfs.DocStore }

// graphKeeper is what a graph mount is built over: the record and the snapshots
// of it. They are two ports because their second implementations are two
// things — a real graph backend snapshots itself and keeps no blob — and one
// type here because a laptop's corpus is where both go.
type graphKeeper interface {
	graphfs.Store
	graphfs.SnapshotStore
}

// graphStore returns the store every graphfs mount keeps its blob in, or nothing
// when this build names no corpus. The kind refuses a mount over nothing rather
// than booting a graph that could keep nothing.
func graphStore(docs vectorfs.DocStore) graphKeeper {
	if docs == nil {
		return nil
	}
	return graphBlobs{docs: docs}
}

// Load hands back the blob one graph was kept as. A name with nothing under it
// reaches graphfs as fs.ErrNotExist, which is a graph that has not been written
// yet rather than a store that failed.
func (b graphBlobs) Load(ctx context.Context, name string) ([]byte, error) {
	return b.docs.Get(ctx, graphPrefix+name)
}

// Save writes the whole graph back.
func (b graphBlobs) Save(ctx context.Context, name string, body []byte) error {
	return b.docs.Put(ctx, graphPrefix+name, body, graphMode)
}

// Keep files one snapshot of a graph under its label.
func (b graphBlobs) Keep(ctx context.Context, graph, label string, body []byte) error {
	return b.docs.Put(ctx, snapshotKey(graph, label), body, graphMode)
}

// Fetch hands back one snapshot. A label nothing was kept under reaches graphfs
// as fs.ErrNotExist, which is the sentinel the port asks for.
func (b graphBlobs) Fetch(ctx context.Context, graph, label string) ([]byte, error) {
	return b.docs.Get(ctx, snapshotKey(graph, label))
}

// Labels names every snapshot kept of one graph, sorted. The corpus lists in no
// order, so the sort is here rather than assumed of it.
func (b graphBlobs) Labels(ctx context.Context, graph string) ([]string, error) {
	under := snapshotKey(graph, "")
	kept, err := b.docs.List(ctx, under)
	if err != nil {
		return nil, err
	}

	labels := make([]string, 0, len(kept))
	for _, one := range kept {
		labels = append(labels, strings.TrimPrefix(one.Key, under))
	}
	slices.Sort(labels)
	return labels, nil
}

// Drop forgets one snapshot.
func (b graphBlobs) Drop(ctx context.Context, graph, label string) error {
	return b.docs.Delete(ctx, snapshotKey(graph, label))
}

// snapshotKey is where one graph's snapshot is filed in the corpus. An empty
// label is the prefix every snapshot of that graph is under, which is what a
// listing walks.
func snapshotKey(graph, label string) string {
	return snapshotPrefix + graph + "/" + label
}
