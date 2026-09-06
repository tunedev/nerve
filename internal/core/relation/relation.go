// Package relation is a workspace's curated graph: the edges someone asserted
// between two resources, as distinct from the edges an extractor derived from
// their contents.
//
// The distinction is the package. An extraction reads text and produces what it
// believes, so it is free to symmetrise an edge, close one transitively, or
// change its mind on the next run. Nothing here believes anything: an edge
// exists because Link was called and stops existing because Unlink was, and
// List answers with those and no others. That is what makes a curated edge
// worth keeping beside a derived one rather than merged into it.
//
// An edge is a subject, a predicate and an object, and the whole of it is its
// address on the tree. Nothing is stored in the file, because a fact needs no
// body: writing one would be a second place to say what the edge is, and two
// writers asserting different edges never touch the same file.
//
// A ref names a resource by its kind and its name — the kernel's addressing, so
// "skill" and "cv" reach the document a library of skills keeps under that
// name. Whether either end exists is not checked. A curated edge is an
// assertion about two names, and a graph that refused an edge to a document not
// written yet would make the order things were created in part of the model.
//
// The graph stores nothing itself. It reads and writes the one FileSystem the
// mount table presents, at paths derived from the scope that owns them, so
// whether the curated edges actually sit on a disk, in a keyspace or in an
// object store is a mount rather than a decision made here.
package relation

import "github.com/tunedev/nerve/internal/core/vfs"

// Ref names one resource an edge can reach: what sort of thing it is, and which
// one of them. Both are flat segments, which is what lets a ref be a place on
// the tree rather than a string something has to parse.
type Ref struct {
	// Kind is the sort of resource, as a library names it — "skill",
	// "blueprint". It is half the address: two kinds can hold a document of
	// one name, and an edge to one of them is not an edge to the other.
	Kind string
	// Name is which resource of that kind, the name it is addressed by.
	Name string
}

// Edge is one curated relation: the subject it was asserted about, what is
// being asserted, and the resource on the other end.
type Edge struct {
	From Ref
	// Predicate is what the subject does to the object — "uses", "supersedes",
	// "produces". It is part of the edge, so one pair related two ways is two
	// edges and neither replaces the other.
	Predicate string
	To        Ref
}

// valid returns fs.ErrInvalid for a ref no edge can reach.
func (r Ref) valid() error {
	if err := vfs.ValidSegment("kind", r.Kind); err != nil {
		return err
	}
	return vfs.ValidSegment("name", r.Name)
}

// String is what a ref is called in an error: the kind and the name, the way it
// stands on the tree.
func (r Ref) String() string { return r.Kind + "/" + r.Name }

// String is what an edge is called in an error, which is the assertion it makes
// read left to right.
func (e Edge) String() string { return e.From.String() + " " + e.Predicate + " " + e.To.String() }
