// Package queuefs mounts a job queue as part of the tree. A message is written
// by writing a path and consumed by reading it, which is the whole of the
// interface a producer or a consumer needs.
//
// The mount holds two places and they mean different things, which is what the
// per-path descriptor is for:
//
//	/jobs          the queue. Reading a message consumes it; names are keys
//	/config        what the queue is. Read as often as you like, written never
//
// The queue is the first kind with two things behind it, and only one of them
// is the record. A message is written to the journal and only then carried to
// the broker,
// because a broker has destructive reads and no replay: losing it has to lose
// nothing. The journal is the system of record and the broker is transport, so
// a write that the broker never hears about is a write that happened, and a
// consumer that arrives later is backfilled from the journal rather than from
// anything the broker kept.
//
// Both sit behind ports. Postgres and RabbitMQ are the shipped sides; the suite
// runs against neither, which is what keeps the semantics assertable with
// nothing started.
package queuefs

import (
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Jobs and Config are the two names a queue mount answers for. Nothing makes
// them and nothing removes them: they are the shape of the mount rather than
// entries in it.
const (
	Jobs   = "jobs"
	Config = "config"
)

// dirMode and fileMode are what an entry reads back as. A prefix under the
// queue is reported as a directory so a caller listing one sees the shape it
// expects; nothing is stored to make that true.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// separator is the delimiter that makes the flat key space read as one level of
// a tree, and what a key's parts are joined by.
const separator = "/"

// FS is one queue seen as part of the tree.
//
// It holds no messages of its own. The journal does, because a queue whose
// state lived in this process would lose it with the process, and the whole
// point of the kind is that nothing does.
type FS struct {
	vfs.NoAccessors

	name    string
	journal Journal
	relay   *Relay
}

// New returns a queue named name, recorded in journal and carried by relay.
//
// The name is what the mount reads back as at its config path. It is not how
// the journal finds its messages: a journal is built for one queue and scopes
// itself, so nothing here has to pass a name into every call.
func New(name string, journal Journal, relay *Relay) *FS {
	return &FS{name: name, journal: journal, relay: relay}
}

// Capabilities reports the queue under the jobs path and a fixed, read-only
// shape everywhere else. This is the descriptor varying inside one mount: the
// same plugin consumes what it hands back at one path and refuses to be written
// at another, and no type could have said both.
func (f *FS) Capabilities(name string) vfs.Caps {
	if spot, _ := locate(name); spot == atQueue || spot == atMessage {
		return vfs.Caps{ReadDestructive: true, ObjectStore: true}
	}
	return vfs.Caps{ReadOnly: true}
}

// spot is where a path falls in the mount's shape.
type spot int

const (
	// outside is a path this mount has no place for.
	outside spot = iota
	// atRoot is the mount itself.
	atRoot
	// atConfig is the path that describes the queue.
	atConfig
	// atQueue is the queue directory itself, which no verb makes or unmakes.
	atQueue
	// atMessage is a name under the queue: a message key, a prefix of one, or
	// neither yet.
	atMessage
)

// locate resolves a path into where it falls and, for a name under the queue,
// the key it is. Paths are cleaned here so a caller is not made to spell what a
// mount table would have cleaned.
func locate(name string) (spot, string) {
	clean := path.Clean(separator + strings.TrimPrefix(name, separator))
	switch {
	case clean == separator:
		return atRoot, ""
	case clean == separator+Config:
		return atConfig, ""
	case clean == separator+Jobs:
		return atQueue, ""
	case strings.HasPrefix(clean, separator+Jobs+separator):
		return atMessage, strings.TrimPrefix(clean, separator+Jobs+separator)
	default:
		return outside, ""
	}
}

// description is what the config path holds: the queue's name, and nothing that
// reaches it. Where the journal and the broker are is a credential's neighbour
// and reads back through the operator surface, which redacts.
func (f *FS) description() []byte { return []byte("queue " + f.name + "\n") }

// info is what the port says about a message.
func (e Entry) info() vfs.FileInfo {
	return vfs.FileInfo{
		Name:    base(e.Key),
		Size:    e.Size,
		Mode:    e.Mode.Perm(),
		ModTime: e.Recorded,
	}
}

// dirInfo is what the port says about a prefix: a directory that exists only
// because a message sits under it, and that nothing was written to make.
func dirInfo(name string) vfs.FileInfo {
	return vfs.FileInfo{Name: base(name), Size: 0, Mode: dirMode}
}

// base is the last element of a key or a path, which is the name an entry
// carries however it was reached.
func base(name string) string { return path.Base(strings.TrimSuffix(name, separator)) }
