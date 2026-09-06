package graphfs

import "context"

// Store is where a graph is kept between processes: one blob under one name.
//
// It is deliberately not a graph. Nothing here traverses, indexes or queries;
// the whole graph is loaded once and written whole, which is what makes a
// bucket and a directory on the disk interchangeable behind this port. A store
// that could answer an expansion itself is a different kind of mount.
//
// A name is one graph's, so two graphs in one store never load each other's
// blob.
//
// Errors are the io/fs sentinels: a name with nothing kept under it is
// fs.ErrNotExist, whatever the disk or the wire called it. That is the ordinary
// first boot and not a failure.
type Store interface {
	// Load returns the blob kept under name.
	Load(ctx context.Context, name string) ([]byte, error)

	// Save keeps body under name, replacing whatever was there.
	Save(ctx context.Context, name string, body []byte) error
}
