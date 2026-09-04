package vectorfs

import "github.com/tunedev/nerve/internal/core/vfs"

// pool is the database side a mount opened, seen only as the thing that gives
// it back. Its Close reports nothing because handing connections back cannot
// fail.
type pool interface{ Close() }

// mount is the index together with the pool this factory opened for it. FS
// speaks to its port and knows nothing about the database, so the lifetime of
// what was opened stays with what opened it.
//
// It is what makes an index reachable by the registry's Release, which collects
// whichever plugin a factory returns implementing io.Closer. Every verb is the
// index's, promoted; only Close is here.
type mount struct {
	*FS

	index pool
}

// holding returns the index fsys as a mount that gives its pool back when it is
// released.
//
// The registry calls this once the server has stopped answering. Until then a
// verb may still be mid-flight on this mount, and an index whose database was
// taken away under it fails a caller that could do nothing about it.
func holding(fsys *FS, index pool) vfs.FileSystem { return &mount{FS: fsys, index: index} }

// Close gives back the pool.
func (m *mount) Close() error {
	m.index.Close()
	return nil
}
