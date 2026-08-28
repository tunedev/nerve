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
	if err := validSegment("user", s.User); err != nil {
		return "", err
	}
	if s.Workspace != "" {
		if err := validSegment("workspace", s.Workspace); err != nil {
			return "", err
		}
	}
	return path.Join("/", root, s.User, s.Workspace, path.Join("/", rest)), nil
}

// validSegment reports whether name can stand as one path segment. The rule is
// the ref-name rule for the same reason: a name that is not flat leaves the
// place it was written into.
func validSegment(kind, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s is empty: %w", kind, fs.ErrInvalid)
	case strings.Contains(name, "/"):
		return fmt.Errorf("%s %q contains a separator: %w", kind, name, fs.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("%s %q names a directory, not a %s: %w", kind, name, kind, fs.ErrInvalid)
	}
	return nil
}
