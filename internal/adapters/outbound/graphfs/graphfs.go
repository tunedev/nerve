// Package graphfs mounts a knowledge graph as part of the tree. A text goes in
// by writing a path and what it said about a thing comes back by reading one,
// which is the whole of the interface a retriever needs.
//
// The mount holds three places and they mean different things, which is what
// the per-path descriptor is for:
//
//	/docs/<key>        the texts. Writing one reads its entities and relations
//	                   into the graph; reading gives the text back
//	/neighbours/<name> one hop out of every entity with that name
//	/config            what the graph is. Read as often as you like, written never
//
// An expansion is a path rather than a parameter for the reason a similarity
// query is: the tree has no parameters, so the name of the thing being asked
// about goes where a name goes and the claims come back the way content does.
//
// Nothing under /neighbours is stored. A name there exists because it is a
// thing that can be asked about, so a listing of /neighbours is empty while a
// read of any name under it answers.
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

// Docs, Neighbours and Config are the three names a graph mount answers for.
// Nothing makes them and nothing removes them: they are the shape of the mount
// rather than entries in it.
const (
	Docs       = "docs"
	Neighbours = "neighbours"
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

	name    string
	edges   int
	store   Store
	extract llm.EntityExtractor

	// mu guards documents and serialises the writes that replace it. A write
	// holds it across the Save, because the blob is one object and two
	// processes-worth of concurrent saves would each write a graph missing the
	// other's change.
	mu        sync.RWMutex
	documents map[string]document
}

// Open returns the graph named name kept in store, read by extract, answering
// an expansion with at most edges claims.
//
// The graph is loaded here, so a store that cannot be read is a graph that
// cannot be opened. Nothing kept under the name yet is a graph with nothing in
// it, which is the first boot.
func Open(ctx context.Context, name string, edges int, store Store, extract llm.EntityExtractor) (*FS, error) {
	documents, err := load(ctx, store, name)
	if err != nil {
		return nil, err
	}
	return &FS{name: name, edges: edges, store: store, extract: extract, documents: documents}, nil
}

// Capabilities reports the texts under the docs path and a fixed, read-only
// shape everywhere else. This is the descriptor varying inside one mount: the
// same plugin takes texts at one path, answers about entities at another and
// refuses to be written at either of the other two, and no type could have said
// all three.
//
// The texts are an object store rather than a tree: a key is a flat name, a
// prefix is a directory only while something sits under it, and Mkdir has
// nothing to make.
func (f *FS) Capabilities(name string) vfs.Caps {
	if spot, _ := locate(name); spot == atCorpus || spot == atDoc {
		return vfs.Caps{ObjectStore: true}
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
	case strings.HasPrefix(clean, separator+Docs+separator):
		return atDoc, strings.TrimPrefix(clean, separator+Docs+separator)
	case strings.HasPrefix(clean, separator+Neighbours+separator):
		return atEntity, strings.TrimPrefix(clean, separator+Neighbours+separator)
	default:
		return outside, ""
	}
}

// description is what the config path holds: what the graph is called and how
// many claims one expansion is answered with. Where the blob is kept is the
// store's business and reads back through the operator surface, which redacts.
func (f *FS) description() []byte {
	return []byte("graph " + f.name + "\nedges " + strconv.Itoa(f.edges) + "\n")
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
		return fmt.Errorf("%s %q: a graph holds %q, %q and %q: %w",
			verb, name, Docs, Neighbours, Config, fs.ErrPermission)
	}
	return fmt.Errorf("%s %q: the shape of the mount: %w", verb, name, fs.ErrPermission)
}
