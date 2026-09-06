// Package workspace derives the paths a user's workspace occupies. The Forge is
// one tenant, so isolation here is namespacing rather than multi-tenancy: a
// workspace is a subtree, and everything inside it is reached by a path derived
// from the scope that owns it.
//
// Every scoped path in the system comes from Path. That is the whole point of
// the package: a second place that spells the layout is a second place that can
// spell it differently, and the difference reads as one user's data appearing
// inside another's.
//
// A caller is handed View rather than the tree, so that deriving the path is the
// scope's job and not the caller's. Path alone contains a caller that uses it;
// View contains one that does not.
package workspace

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Scope names the owner of a subtree. A scope with a workspace is workspace
// scope; a scope with a user and no workspace is user scope, the subtree
// holding every workspace that user has.
type Scope struct {
	User      string
	Workspace string
}

// Path returns the rooted path rest occupies inside s under root, where root is
// the area the path belongs to — the files mount, the key space, the queue.
// The same scope derives the same suffix under every root, which is what lets
// one workspace be created or deleted across all of them.
//
// rest is contained rather than trusted: it is resolved against the scope root,
// so a spelling that climbs lands on the root instead of above it. A user or
// workspace that is not a flat name is refused with fs.ErrInvalid, because a
// segment carrying a separator is a scope that can name another one.
//
// The containment here is lexical, and it is only the first of two layers. A
// derived path is a name; what the store behind it resolves that name to is
// enforced by the plugin mounted under the scope, at its own boundary.
func (s Scope) Path(root, rest string) (string, error) {
	if err := s.valid(); err != nil {
		return "", err
	}
	return path.Join("/", root, s.User, s.Workspace, path.Join("/", rest)), nil
}

// valid reports whether s names segments that each stand as one name. It is
// here rather than inside Path because a caller that reaches the tree without
// deriving a path down to it still needs the same answer, and asking it twice
// in two spellings is how the two come to disagree.
//
// A scope with no workspace is user scope rather than an invalid one.
func (s Scope) valid() error {
	if err := vfs.ValidSegment("user", s.User); err != nil {
		return err
	}
	if s.Workspace == "" {
		return nil
	}
	return vfs.ValidSegment("workspace", s.Workspace)
}

// Locate is the inverse of Path: it returns the scope that name stands in under
// root, and what name is inside that scope.
//
// It is here rather than at whoever needs it for the reason Path is: a caller
// that took a scope out of a path its own way would be the second place that
// spells the layout, and the two would disagree the day one of them changed.
//
// name is cleaned first, so a key that lost the leading slash a path carries —
// which is what a queue hands back — names the same place as the path did.
//
// A name outside root is refused with fs.ErrNotExist, and so is one that names
// no workspace: a path with only a user segment is the subtree holding every
// workspace that user has, not a place inside one.
func Locate(root, name string) (Scope, string, error) {
	prefix := path.Clean("/" + root)
	if prefix != "/" {
		prefix += "/"
	}
	rest, inside := strings.CutPrefix(path.Clean("/"+name), prefix)
	if !inside {
		return Scope{}, "", fmt.Errorf("%q is not under %q: %w", name, root, fs.ErrNotExist)
	}

	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return Scope{}, "", fmt.Errorf("%q names no workspace under %q: %w", name, root, fs.ErrNotExist)
	}
	s := Scope{User: parts[0], Workspace: parts[1]}
	if len(parts) == 2 {
		return s, "", nil
	}
	return s, parts[2], nil
}
