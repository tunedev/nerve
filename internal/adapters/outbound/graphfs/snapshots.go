package graphfs

import "context"

// SnapshotStore is where a graph's past is kept: the whole of one graph as it
// stood, under a label somebody chose.
//
// It is a second port rather than four more verbs on Store, because the
// alternative implementation is not another place to put a blob. A real graph
// backend snapshots itself — a dump, a copy-on-write clone, a backup the server
// takes — and implements nothing of Store's load-and-save-the-whole-thing. The
// two ports separate because their second implementations do.
//
// A label is one name and not a path. What it means is the caller's: an instant,
// a release, the thing about to be tried.
//
// Errors are the io/fs sentinels: a label with nothing kept under it is
// fs.ErrNotExist, whatever the disk or the wire called it.
type SnapshotStore interface {
	// Keep files body as the graph's snapshot under label, replacing whatever
	// that label held.
	Keep(ctx context.Context, graph, label string, body []byte) error

	// Fetch returns the snapshot kept under label.
	Fetch(ctx context.Context, graph, label string) ([]byte, error)

	// Labels names every snapshot kept of the graph, sorted. A graph nothing
	// has been kept of has no labels, which is not an error.
	Labels(ctx context.Context, graph string) ([]string, error)

	// Drop forgets the snapshot under label, failing with fs.ErrNotExist when
	// there is none.
	Drop(ctx context.Context, graph, label string) error
}
