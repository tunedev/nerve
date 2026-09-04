// Package vectorfs mounts a similarity index as part of the tree. A document is
// added by writing a path and found by reading one, which is the whole of the
// interface an indexer or a retriever needs.
//
// The mount holds three places and they mean different things, which is what
// the per-path descriptor is for:
//
//	/docs/<key>    the corpus. Writing a document embeds it; reading gives it back
//	/query/<text>  the question. Reading it answers with the corpus, ranked
//	/config        what the index is. Read as often as you like, written never
//
// A query is a path rather than a parameter because the tree has no parameters.
// Everything a caller can say to a mount, it says by naming a path, so the
// question goes where a name goes and the answer comes back the way content
// does. The mount table cleans paths before a plugin sees them, so a question
// spelled with "//" or ".." asks the cleaned one; a question is a sentence and
// this has not come up.
//
// Nothing under /query is stored. A name there exists because it is a question
// that can be asked, so a listing of /query is empty while a read of any name
// under it answers. That is the honest shape: there are no queries to enumerate,
// only a corpus to ask about.
//
// The vectors are made through the gateway, so nothing here holds a model
// client and which endpoint embeds is a line of config. A mount with no
// embedding endpoint behind it is refused where it is built rather than on the
// first document it is given.
package vectorfs

import (
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Docs, Query and Config are the three names an index mount answers for.
// Nothing makes them and nothing removes them: they are the shape of the mount
// rather than entries in it.
const (
	Docs   = "docs"
	Query  = "query"
	Config = "config"
)

// dirMode and fileMode are what an entry reads back as. A prefix under the
// corpus is reported as a directory so a caller listing one sees the shape it
// expects; nothing is stored to make that true.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// separator is the delimiter that makes the flat key space read as one level of
// a tree, and what a key's parts are joined by.
const separator = "/"

// FS is one similarity index seen as part of the tree.
//
// It holds no documents of its own. The index does, because a corpus whose
// state lived in this process would lose it with the process, and the cost of
// filling it again is a model call per document.
type FS struct {
	vfs.NoAccessors

	name       string
	dimension  int
	neighbours int
	index      Index
	embed      llm.Embedder
}

// New returns an index named name over documents embedded to dimension by
// embed, answering with at most neighbours documents per question.
//
// The name is what the mount reads back as at its config path. It is not how
// the index finds its documents: an index is built for one corpus and scopes
// itself, so nothing here has to pass a name into every call.
func New(name string, dimension, neighbours int, index Index, embed llm.Embedder) *FS {
	return &FS{name: name, dimension: dimension, neighbours: neighbours, index: index, embed: embed}
}

// Capabilities reports the corpus under the docs path and a fixed, read-only
// shape everywhere else. This is the descriptor varying inside one mount: the
// same plugin takes documents at one path, answers questions at another and
// refuses to be written at either of the other two, and no type could have said
// all three.
//
// The corpus is an object store rather than a tree: a key is a flat name, a
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
	// atConfig is the path that describes the index.
	atConfig
	// atCorpus is the documents directory itself, which no verb makes or unmakes.
	atCorpus
	// atDoc is a name under the corpus: a document key, a prefix of one, or
	// neither yet.
	atDoc
	// atQuery is the questions directory itself, which holds nothing.
	atQuery
	// atQuestion is a name under it, which is the question.
	atQuestion
)

// locate resolves a path into where it falls and, for a name under the corpus
// or under the questions, what that name says: a document key, or the question
// to ask.
//
// The remainder is taken whole rather than split, so a question with a
// separator in it is one question and not a path into something.
func locate(name string) (spot, string) {
	clean := path.Clean(separator + strings.TrimPrefix(name, separator))
	switch {
	case clean == separator:
		return atRoot, ""
	case clean == separator+Config:
		return atConfig, ""
	case clean == separator+Docs:
		return atCorpus, ""
	case clean == separator+Query:
		return atQuery, ""
	case strings.HasPrefix(clean, separator+Docs+separator):
		return atDoc, strings.TrimPrefix(clean, separator+Docs+separator)
	case strings.HasPrefix(clean, separator+Query+separator):
		return atQuestion, strings.TrimPrefix(clean, separator+Query+separator)
	default:
		return outside, ""
	}
}

// description is what the config path holds: what the index is called, how wide
// its vectors are and how many documents a question is answered with. Where the
// database is is a credential's neighbour and reads back through the operator
// surface, which redacts.
func (f *FS) description() []byte {
	return []byte("index " + f.name +
		"\ndimension " + strconv.Itoa(f.dimension) +
		"\nneighbours " + strconv.Itoa(f.neighbours) + "\n")
}

// info is what the index says about a document.
func (e Entry) info() vfs.FileInfo {
	return vfs.FileInfo{
		Name:    base(e.Key),
		Size:    e.Size,
		Mode:    e.Mode.Perm(),
		ModTime: e.Written,
	}
}

// dirInfo is what the port says about a prefix: a directory that exists only
// because a document sits under it, and that nothing was written to make.
func dirInfo(name string) vfs.FileInfo {
	return vfs.FileInfo{Name: base(name), Size: 0, Mode: dirMode}
}

// base is the last element of a key or a path, which is the name an entry
// carries however it was reached.
func base(name string) string { return path.Base(strings.TrimSuffix(name, separator)) }
