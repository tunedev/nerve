package version

import (
	"context"
	"errors"

	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// ID identifies one version: the digest of the manifest saying what that
// version holds. It is cas.Digest under the name this domain uses for it, not a
// second type over the same idea — a version is one object, so its ID is what
// the object store already calls that object.
//
// Being the same type is what makes it checked. An ID is computed from content
// or parsed from a recorded spelling, so a ref store that reads one back has
// already refused a value that was never a digest, rather than passing a string
// on for the first reader of a version to fail on.
//
// The zero ID names no version, which is what an unset ref resolves to, and
// what a working copy taken from nothing stands at.
type ID = cas.Digest

// ErrConflict reports that a ref did not hold the value the writer expected, so
// the write was refused. It is the reason every write names an expected value:
// two writers racing on one ref must not both come away believing they won.
var ErrConflict = errors.New("ref moved under the write")

// RefStore keeps the named refs of a versioned tree — a name, and the version
// it stands for — and moves one only from a value the caller names.
//
// A second implementation is nameable, which is why this is a port: refs on the
// local tree for the laptop, refs in a Postgres table where more than one
// process has to agree on who won a race.
//
// Names are flat: a non-empty string with no "/" in it, and neither "." nor
// "..". Scoping a ref to a workspace or a user is the mount point's business,
// not the name's, so a scoped store is the same store over a different root.
//
// Every write is a compare-and-set. There is no verb that sets a ref without
// saying what it expected to find, because the caller that does not care what
// it is overwriting is the caller that loses someone else's commit.
//
// Errors are the io/fs sentinels — fs.ErrNotExist for a ref that stands for
// nothing, fs.ErrInvalid for a name that is not one — plus ErrConflict, and are
// matched with errors.Is.
type RefStore interface {
	// Resolve returns the version name stands for, or fs.ErrNotExist if
	// nothing does.
	Resolve(ctx context.Context, name string) (ID, error)

	// List returns every name that stands for a version, sorted.
	List(ctx context.Context) ([]string, error)

	// Set points name at next, and fails with ErrConflict unless name
	// currently stands for expected. The zero expected ID means the ref must
	// not exist yet, which is how one is created; the zero next ID is not a
	// deletion and is refused.
	Set(ctx context.Context, name string, expected, next ID) error

	// Delete unsets name, and fails with ErrConflict unless name currently
	// stands for expected. Expecting the zero ID is the one mismatch that is
	// not a conflict: there is no ref to unset, so it is fs.ErrNotExist.
	Delete(ctx context.Context, name string, expected ID) error
}

// ValidateRefName returns fs.ErrInvalid for a name no ref store will accept.
// The rule is named here rather than in each implementation so that a name
// refused by one store is refused by all of them, and so that a name can never
// carry a path separator into a store that keeps refs as files.
func ValidateRefName(name string) error {
	return vfs.ValidSegment("ref name", name)
}
