// Package graphfs mounts a knowledge graph as part of the tree. A text goes in
// by writing a path and what it said about a thing comes back by reading one,
// which is the whole of the interface a retriever needs.
//
// The mount holds five places and they mean different things, which is what
// the per-path descriptor is for:
//
//	/docs/<key>        the texts. Writing one reads its entities and relations
//	                   into the graph; reading gives the text back
//	/neighbours/<name> every claim within depth hops of the entities with that
//	                   name, the best supported first
//	/mentions/<name>   the texts naming that entity, the ones that say most
//	                   about it first
//	/snapshots/<label> the graph as it stood. Writing one takes it, reading one
//	                   describes it, removing one forgets it
//	/restore           write a label to set the graph back to that snapshot
//	/config            what the graph is. Read as often as you like, written never
//
// An expansion is a path rather than a parameter for the reason a similarity
// query is: the tree has no parameters, so the name of the thing being asked
// about goes where a name goes and the claims come back the way content does.
//
// Nothing under /neighbours or /mentions is stored. A name there exists because
// it is a thing that can be asked about, so a listing of either is empty while a
// read of any name under one answers.
//
// The two answer the same question from opposite ends. An expansion is what the
// graph says about a thing; the mentions are which texts said it. A retriever
// filling a context window wants the texts, and it wants them as a ranking it
// can fuse with another ranking over the same keys, which is why they come back
// ordered and keyed the way the corpus is keyed.
//
// The entities and relations are read through the gateway, so nothing here
// holds a model client and which endpoint reads a text is a line of config. A
// mount with no extracting endpoint behind it is refused where it is built
// rather than on the first text it is given.
//
// The graph is held whole in memory and kept as one blob. That is what the
// Store port is: the record is the blob, and every verb that changes the graph
// writes the whole of it before the change is visible here. It is a laptop's
// graph, bounded by what one process can hold and rewritten in full on every
// write; a graph that outgrows either is what a real graph backend is for.
//
// A snapshot is a second port rather than more verbs on that one, because the
// real graph backend snapshots itself and keeps no blob. A restore is a change
// like any other: it goes through the same commit, so a restore the record
// refused is a restore this mount did not make.
package graphfs

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Docs, Neighbours, Snapshots, Restore and Config are the names a graph mount
// answers for. Nothing makes them and nothing removes them: they are the shape
// of the mount rather than entries in it.
const (
	Docs       = "docs"
	Neighbours = "neighbours"
	Mentions   = "mentions"
	Snapshots  = "snapshots"
	Restore    = "restore"
	Config     = "config"
)

// dirMode and fileMode are what an entry reads back as. A prefix under the
// texts is reported as a directory so a caller listing one sees the shape it
// expects; nothing is stored to make that true.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// separator is the delimiter that makes the flat key space read as one level of
// a tree, and what a key's parts are joined by.
const separator = "/"

// FS is one knowledge graph seen as part of the tree.
//
// It holds the graph itself, which is what "in-memory" means: every read is
// answered from this process and no expansion costs a round trip. What it does
// not hold is the record — that is the blob in the Store, and the field here is
// a copy of it that no verb changes until the blob has taken the change.
type FS struct {
	vfs.NoAccessors

	name      string
	edges     int
	depth     int
	store     Store
	snapshots SnapshotStore
	extract   llm.EntityExtractor

	// mu guards the graph and serialises the writes that replace it. A write
	// holds it across the Save, because the blob is one object and two
	// processes-worth of concurrent saves would each write a graph missing the
	// other's change.
	mu    sync.RWMutex
	graph graph
}

// Open returns the graph named name kept in store and snapshotted into
// snapshots, read by extract, answering an expansion with at most edges claims
// from up to depth hops out, and a mentions query with at most edges texts.
//
// The graph is loaded here, so a store that cannot be read is a graph that
// cannot be opened. Nothing kept under the name yet is a graph with nothing in
// it, which is the first boot.
func Open(ctx context.Context, name string, edges, depth int, store Store, snapshots SnapshotStore, extract llm.EntityExtractor) (*FS, error) {
	held, err := load(ctx, store, name)
	if err != nil {
		return nil, err
	}
	return &FS{name: name, edges: edges, depth: depth, store: store, snapshots: snapshots, extract: extract, graph: held}, nil
}

// Capabilities reports three different things inside one mount, which is what
// the per-path descriptor is for: the texts are an object store, the snapshots
// and the restore path are an ordinary read-write tree, and what the graph says
// about itself and its entities is read-only. No type could have said all
// three.
//
// The texts are an object store rather than a tree: a key is a flat name, a
// prefix is a directory only while something sits under it, and Mkdir has
// nothing to make. A snapshot label is not a key: it is one name, and a label
// with a separator in it is no label at all.
func (f *FS) Capabilities(name string) vfs.Caps {
	switch spot, _ := locate(name); spot {
	case atCorpus, atDoc:
		return vfs.Caps{ObjectStore: true}
	case atSnapshots, atSnapshot, atRestore:
		return vfs.Caps{}
	default:
		return vfs.Caps{ReadOnly: true}
	}
}

// spot is where a path falls in the mount's shape.
type spot int

const (
	// outside is a path this mount has no place for.
	outside spot = iota
	// atRoot is the mount itself.
	atRoot
	// atConfig is the path that describes the graph.
	atConfig
	// atCorpus is the texts directory itself, which no verb makes or unmakes.
	atCorpus
	// atDoc is a name under the texts: a key, a prefix of one, or neither yet.
	atDoc
	// atNeighbours is the expansions directory itself, which holds nothing.
	atNeighbours
	// atEntity is a name under it, which is the entity being asked about.
	atEntity
	// atMentions is the mentions directory itself, which holds nothing.
	atMentions
	// atMentioned is a name under it, which is the entity the texts are ranked
	// against.
	atMentioned
	// atSnapshots is the snapshots directory itself, which lists the labels.
	atSnapshots
	// atSnapshot is a label under it: a graph as it stood, or a label nothing
	// has been kept under yet.
	atSnapshot
	// atRestore is the path a label is written to to set the graph back.
	atRestore
)

// locate resolves a path into where it falls and, for a name under the texts or
// under the expansions, what that name says: a key, or the entity to expand.
//
// The remainder is taken whole rather than split, so an entity whose name has a
// separator in it is one name and not a path into something.
func locate(name string) (spot, string) {
	clean := path.Clean(separator + strings.TrimPrefix(name, separator))
	switch {
	case clean == separator:
		return atRoot, ""
	case clean == separator+Config:
		return atConfig, ""
	case clean == separator+Docs:
		return atCorpus, ""
	case clean == separator+Neighbours:
		return atNeighbours, ""
	case clean == separator+Mentions:
		return atMentions, ""
	case clean == separator+Snapshots:
		return atSnapshots, ""
	case clean == separator+Restore:
		return atRestore, ""
	case strings.HasPrefix(clean, separator+Docs+separator):
		return atDoc, strings.TrimPrefix(clean, separator+Docs+separator)
	case strings.HasPrefix(clean, separator+Neighbours+separator):
		return atEntity, strings.TrimPrefix(clean, separator+Neighbours+separator)
	case strings.HasPrefix(clean, separator+Mentions+separator):
		return atMentioned, strings.TrimPrefix(clean, separator+Mentions+separator)
	case strings.HasPrefix(clean, separator+Snapshots+separator):
		return atSnapshot, strings.TrimPrefix(clean, separator+Snapshots+separator)
	default:
		return outside, ""
	}
}

// description is what the config path holds: what the graph is called, how
// much of an answer it hands back and how far out an expansion reaches. Where
// the blob is kept is the store's business and reads back through the operator
// surface, which redacts.
func (f *FS) description() []byte {
	return []byte("graph " + f.name + "\nedges " + strconv.Itoa(f.edges) + "\ndepth " + strconv.Itoa(f.depth) + "\n")
}

// dirInfo is what a prefix reads back as: a directory that exists only because
// a text sits under it, and that nothing was written to make.
func dirInfo(name string) vfs.FileInfo {
	return vfs.FileInfo{Name: base(name), Size: 0, Mode: dirMode}
}

// base is the last element of a key or a path, which is the name an entry
// carries however it was reached.
func base(name string) string { return path.Base(strings.TrimSuffix(name, separator)) }

// refuse is what every mutating verb says about a path that is not under the
// texts. The mount's shape is fixed, so the refusal is a permission and not a
// missing entry: there is nothing a caller could create first to make it work.
func (f *FS) refuse(verb, name string, where spot) error {
	if where == outside {
		return fmt.Errorf("%s %q: a graph holds %q, %q, %q, %q, %q and %q: %w",
			verb, name, Docs, Neighbours, Mentions, Snapshots, Restore, Config, fs.ErrPermission)
	}
	return fmt.Errorf("%s %q: the shape of the mount: %w", verb, name, fs.ErrPermission)
}
