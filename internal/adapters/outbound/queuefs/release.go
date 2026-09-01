package queuefs

import "github.com/tunedev/nerve/internal/core/vfs"

// pool and connection are the two sides a mount opened, seen only as the thing
// that gives them back. A pool's Close reports nothing because handing
// connections back cannot fail; a broker's does, because a server that never
// acknowledges the close is worth saying out loud.
type (
	pool       interface{ Close() }
	connection interface{ Close() error }
)

// mount is the queue together with the journal pool and the broker connection
// this factory opened for it. FS speaks to its two ports and knows nothing
// about either, so the lifetime of what was opened stays with what opened it.
//
// It is what makes a queue reachable by the registry's Release, which collects
// whichever plugin a factory returns implementing io.Closer. Every verb is the
// queue's, promoted; only Close is here.
type mount struct {
	*FS

	journal pool
	broker  connection
}

// holding returns the queue fsys as a mount that gives journal and broker back
// when it is released.
func holding(fsys *FS, journal pool, broker connection) vfs.FileSystem {
	return &mount{FS: fsys, journal: journal, broker: broker}
}

// Close gives back the broker connection and then the journal pool, the reverse
// of the order the factory opened them. The pool is given back whatever the
// broker said: a connection that will not close is not a reason to leave a
// database pool open.
//
// The registry calls this once the server has stopped answering. Until then a
// verb may still be mid-flight on this mount, and a queue whose journal was
// taken away under it fails a caller that could do nothing about it.
func (m *mount) Close() error {
	refused := m.broker.Close()
	m.journal.Close()
	return refused
}
