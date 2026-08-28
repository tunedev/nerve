// Package kv is a workspace's scratch key space: the state an agent puts aside
// under a namespace of its own and fetches back later.
//
// It is scratch because nothing here is a system of record. A value is written
// and read whole under a name the caller chose, there is no history and no
// index over it, and what it holds is a cursor, a partial result, a note to the
// next turn — the things that would otherwise live in a process that is about
// to end.
//
// The namespace is what makes it safe to share one workspace. A key name means
// one value per namespace, so two agents working the same workspace under
// names of their own never read each other's state, and neither of them has to
// know the other exists.
//
// The space stores nothing itself. It reads and writes the one FileSystem the
// mount table presents, at paths derived from the scope that owns them, so
// whether the scratch state actually sits in a keyspace, on a disk or in an
// object store is a mount rather than a decision made here. Both shapes work
// unchanged: a namespace is a directory on a tree and a prefix in a keyspace,
// and Put makes the first and asks nothing of the second.
package kv

import (
	"fmt"
	"io/fs"
	"strings"
)

// validSegment returns fs.ErrInvalid for a name no namespace or key can have.
// The rule is the scope's flat-segment rule for the same reason: a name
// carrying a separator names a place inside another one, and "." or ".." is a
// spelling that resolves away and lands on a key nobody asked for.
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
