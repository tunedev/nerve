// Package kvfs is a namespaced key-value store behind the port's verbs. It is
// what sessions and agent state live in: a value is fetched and replaced whole,
// under a key that is the path a caller spelled.
//
// It claims ObjectStore at every path, because a keyspace is not a tree. A name
// is a prefix: nothing has to be made before something is written under one,
// Mkdir has nothing to make, and Stat answers for keys and never for the
// prefixes above them. That descriptor is what the conformance suite reads it
// against, so the cases that assume directories are skipped and the ones that
// assert prefix semantics run.
//
// Nothing optional is answered. A value moves whole in one call, so there is no
// stream over it and nothing to cut in place.
package kvfs

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirMode and fileMode are what an entry is described with. A key is a file;
// dirMode belongs to a prefix a listing synthesized, which exists only because
// keys are under it.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// FS is one namespace of a store. Two mounts over one store hold separate
// entries when their namespaces differ, which is what the namespace is for:
// session state and agent state share a keyspace and never each other's keys.
//
// There is no lock here. The store owns whatever concurrency its keyspace needs,
// and a verb that reads a value and writes it back — Chmod, Rename — is not
// atomic across the port, because the port has no transaction to make it one.
type FS struct {
	vfs.NoAccessors

	store Store
	// under is what every key of this mount starts with: the namespace and one
	// separator, or nothing at all when the mount has no namespace.
	under string
}

// New returns the mount holding namespace in store.
func New(store Store, namespace string) *FS {
	return &FS{store: store, under: namespaceKey(namespace)}
}

// Capabilities reports a keyspace, which is what kvfs is at every path.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{ObjectStore: true} }

// namespaceKey is what a namespace setting means as a key prefix: cleaned, with
// one separator after it, and nothing at all when it names nothing.
func namespaceKey(namespace string) string {
	clean := strings.Trim(path.Clean("/"+namespace), "/")
	if clean == "" {
		return ""
	}
	return clean + "/"
}

// relative resolves name lexically to the part below the mount's root, so a
// caller is not made to clean paths a mount table would have cleaned. The root
// is what is left of a path that names nothing below it.
func relative(name string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(name, "/")), "/")
}

// key is the store key name addresses. The root has none: it is the prefix every
// key of the mount is under rather than a key itself, which is what makes it the
// one path no verb reads, removes or moves.
func (f *FS) key(name string) (string, error) {
	rest := relative(name)
	if rest == "" {
		return "", fmt.Errorf("%q: the root is a prefix and not a key: %w", name, fs.ErrInvalid)
	}
	return f.under + rest, nil
}

// prefix is what a listing of name matches: every key beneath it, and never the
// key at it.
func (f *FS) prefix(name string) string {
	rest := relative(name)
	if rest == "" {
		return f.under
	}
	return f.under + rest + "/"
}

// info is what the port says about the key stored under name.
func info(name string, one Entry) vfs.FileInfo {
	return vfs.FileInfo{
		Name:    path.Base(relative(name)),
		Size:    one.Size,
		Mode:    one.Mode,
		ModTime: one.ModTime,
	}
}
