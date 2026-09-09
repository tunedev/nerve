// Package hybridfs mounts the fusion of several rankings as part of the tree. A
// question is asked by reading a path and the answer is what every ranker
// behind the mount thought, folded into one order.
//
// The mount holds two places:
//
//	/query/<text>  the question. Reading it answers with the fused ranking
//	/config        what the fusion is. Read as often as you like, written never
//
// A question is a path rather than a parameter for the reason it is in an
// index: the tree has no parameters, so the question goes where a name goes and
// the answer comes back the way content does.
//
// Nothing under /query is stored, so a listing of it is empty while a read of
// any name under it answers.
//
// # Reciprocal Rank Fusion
//
// A key's standing is the sum, over the rankers that named it, of 1/(damping +
// its rank there), counting ranks from one. Nothing else is read: not a
// similarity, not a claim count, not how many rows a ranker returned.
//
// That is the whole reason to fuse this way. A cosine distance and a count of
// claims are numbers on different scales, and any arithmetic over both needs
// somebody to decide what one is worth in the other — a calibration that has to
// be redone whenever either ranker changes. Ranks have no scale. A ranker's
// first row is its first row whatever it measured, so a ranker can be swapped,
// retuned, or replaced by a different kind of index without touching anything
// here.
//
// The damping constant is what stops one ranker's first place from settling the
// answer: with it, the gap between rank one and rank two is a fraction of the
// gap between naming a key and not naming it, so a key that two rankers both
// placed modestly beats a key only one of them put first. That agreement is the
// signal fusion exists to find.
//
// # What a ranker is
//
// A ranking read from the tree: a path that answers with a heading line naming
// its columns and then one row per result, best first, whose first
// tab-separated field is the key. An index answers that shape at /query and a
// graph at /mentions, and so would any later kind that ranks.
//
// The sources are read through the tree rather than built here, which is what
// makes them the mounts themselves rather than second copies of them. A graph
// holds its edges in memory; a private second graph would answer from the blob
// it loaded at boot and never see a text written through the mount.
package hybridfs

import (
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Query and Config are the two names a fusion mount answers for. Nothing makes
// them and nothing removes them: they are the shape of the mount rather than
// entries in it.
const (
	Query  = "query"
	Config = "config"
)

// dirMode and fileMode are what an entry reads back as.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// separator is what a path's parts are joined by.
const separator = "/"

// Source is one ranking the fusion reads: a name to report it under, and the
// path in the tree it answers questions at. The path is a directory; a question
// is a name under it.
type Source struct {
	Name string
	Path string
}

// FS is one fusion seen as part of the tree.
//
// It holds no ranking of its own and stores nothing. What it answers is what
// its sources answered, in an order neither of them chose.
type FS struct {
	vfs.NoAccessors

	name    string
	damping int
	results int
	sources []Source
	// tree is where the sources are read from. It is a function because a
	// fusion mount is part of the tree it reads: the table exists only once
	// every mount in it has been built, and this one is one of them.
	tree func() vfs.FileSystem
}

// New returns a fusion named name over sources, folding their rankings with the
// given damping constant and answering with at most results keys. The rankings
// are read from the tree the function hands back, which is the table this mount
// is itself in.
func New(name string, damping, results int, sources []Source, tree func() vfs.FileSystem) *FS {
	return &FS{name: name, damping: damping, results: results, sources: sources, tree: tree}
}

// Capabilities reports a read-only mount everywhere. A fusion has nothing of
// its own to write to: every key it names belongs to a source, and the place to
// change one is the mount that holds it.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{ReadOnly: true} }

// spot is where a path falls in the mount's shape.
type spot int

const (
	// outside is a path this mount has no place for.
	outside spot = iota
	// atRoot is the mount itself.
	atRoot
	// atConfig is the path that describes the fusion.
	atConfig
	// atQuery is the questions directory itself, which holds nothing.
	atQuery
	// atQuestion is a name under it, which is the question.
	atQuestion
)

// locate resolves a path into where it falls and, for a name under the
// questions, the question to ask.
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
	case clean == separator+Query:
		return atQuery, ""
	case strings.HasPrefix(clean, separator+Query+separator):
		return atQuestion, strings.TrimPrefix(clean, separator+Query+separator)
	default:
		return outside, ""
	}
}

// description is what the config path holds: what the fusion is called, the
// constant its ranks are damped by, how many keys an answer carries, and which
// rankings it reads in which order. None of them is a credential, and which
// rankings stand behind an answer is the first thing anyone asks when one looks
// wrong.
func (f *FS) description() []byte {
	spelled := make([]string, 0, len(f.sources))
	for _, one := range f.sources {
		spelled = append(spelled, one.Name+atPath+one.Path)
	}
	return []byte("hybrid " + f.name +
		"\ndamping " + strconv.Itoa(f.damping) +
		"\nresults " + strconv.Itoa(f.results) +
		"\nsources " + strings.Join(spelled, betweenSources) + "\n")
}

// dirInfo is what one of the mount's own directories reads back as.
func dirInfo(name string) vfs.FileInfo {
	return vfs.FileInfo{Name: name, Size: 0, Mode: dirMode}
}

// refuse is what every mutating verb says. A fusion holds nothing of its own,
// so the refusal is a permission and not a missing entry: there is nothing a
// caller could create first to make it work.
func refuse(verb, name string) error {
	return fmt.Errorf("%s %q: a fusion holds %q and %q, and neither is written: %w",
		verb, name, Query, Config, fs.ErrPermission)
}
