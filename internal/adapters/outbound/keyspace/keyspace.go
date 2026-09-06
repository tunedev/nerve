// Package keyspace holds the lexical rules of a flat keyspace: the shape a
// mount takes when a name is a key rather than a path through directories.
//
// It is what ObjectStore means in practice. A name is a prefix, so nothing has
// to be made before something is written under one; a key and a prefix can
// share a name; and a listing of that shared name answers with the key. The
// conformance suite asserts the rule once, and this states it once, so a mount
// of this kind gets it by using it rather than by copying it.
//
// It is not a port. There is no second implementation of a lexical rule: a
// plugin keeps its own store and its own locking and reaches here only for the
// names.
package keyspace

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// DirMode is what a prefix a listing synthesized is described with. A prefix
// exists only because entries are under it, so there is nothing at one to chmod.
const DirMode = fs.ModeDir | 0o755

// Relative resolves name lexically to the part below the mount's root, so a
// caller is not made to clean paths a mount table would have cleaned. The root
// is what is left of a path that names nothing below it.
func Relative(name string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(name, "/")), "/")
}

// Key is the entry name addresses. The root has none: it is the prefix every
// entry of the mount is under rather than an entry itself, which is what makes
// it the one path no verb reads, removes or moves.
func Key(name string) (string, error) {
	rest := Relative(name)
	if rest == "" {
		return "", fmt.Errorf("%q: the root is a prefix and not an entry: %w", name, fs.ErrInvalid)
	}
	return rest, nil
}

// Prefix is what a listing of name matches: every entry beneath it, and never
// the entry at it. The root's prefix is empty, which is every entry of the mount.
func Prefix(name string) string {
	rest := Relative(name)
	if rest == "" {
		return ""
	}
	return rest + "/"
}

// Fold adds one entry to what a listing answers, where rest is what follows the
// listed prefix. The entry either names a child outright, or names one segment
// of a prefix that exists because it and others are under it.
//
// An entry and a prefix can share a name, and the entry is the one that wins:
// it describes itself, where a prefix is only ever described by what is beneath
// it.
func Fold(children map[string]vfs.FileInfo, rest string, one vfs.FileInfo) {
	base, _, deeper := strings.Cut(rest, "/")
	if !deeper {
		one.Name = base
		children[base] = one
		return
	}

	existing, listed := children[base]
	if listed && !existing.IsDir() {
		return
	}
	if one.ModTime.After(existing.ModTime) {
		existing = vfs.FileInfo{Name: base, Mode: DirMode, ModTime: one.ModTime}
	}
	children[base] = existing
}
