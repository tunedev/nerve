package main

import (
	"context"
	"io/fs"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// graphPrefix is what a graph's blob is filed under in the corpus, so a graph
// and a vector index that happen to share a name do not share a key. It is a
// namespace and not a guarantee: the corpus is one keyspace, and an operator who
// names an index "graphs" gets what they asked for.
const graphPrefix = "graphs/"

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

// graphStore returns the store every graphfs mount keeps its blob in, or nothing
// when this build names no corpus. The kind refuses a mount over nothing rather
// than booting a graph that could keep nothing.
func graphStore(docs vectorfs.DocStore) graphfs.Store {
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
