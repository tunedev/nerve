package version

import "context"

// WorkingCopy is the mutable tree a versioner versions, said in the domain's
// vocabulary rather than the mount table's: the content at its paths, and the
// version it was taken from.
//
// Where it stands is the state that makes it a working copy rather than a
// directory. A tree alone can only say how its content differs from a ref; a
// tree that remembers what it was taken from can also say that the ref moved
// underneath it, which is the difference between a commit that refuses and a
// commit that silently drops someone else's version.
//
// A second implementation is nameable, which is why this is a port: the tree
// under a mount with where it stands beside it for a single nerved, and the
// same tree with where it stands in the Postgres table that holds the refs, for
// a workspace more than one process opens. That is the same split RefStore
// took, for the same reason — a copy and the refs it is compared against have
// to agree about who won a race.
//
// Paths are relative to the copy's root, with no leading separator, which is
// how a version records them.
type WorkingCopy interface {
	// Walk visits every file in the copy, with its content. A copy that is not
	// there yet holds no files rather than failing: the tree of a version that
	// has never been committed is empty.
	Walk(ctx context.Context, visit func(path string, data []byte) error) error

	// Write replaces the content at path, making whatever it needs on the way.
	Write(ctx context.Context, path string, data []byte) error

	// Remove deletes path.
	Remove(ctx context.Context, path string) error

	// Base returns the version the copy was taken from, and the zero ID when it
	// was taken from none.
	Base(ctx context.Context) (ID, error)

	// SetBase records that the copy now stands at id. It is not a
	// compare-and-set: a copy has one holder, and the ref it is compared
	// against is where racing is decided.
	SetBase(ctx context.Context, id ID) error
}
