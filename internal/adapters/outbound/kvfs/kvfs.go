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
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// fileMode is what a key is described with. A prefix a listing synthesized is
// described with keyspace.DirMode.
const fileMode = 0o644

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

// key is the store key name addresses, which is the mount's namespace and the
// keyspace key under it.
func (f *FS) key(name string) (string, error) {
	rest, err := keyspace.Key(name)
	if err != nil {
		return "", err
	}
	return f.under + rest, nil
}

// prefix is what a listing of name matches: every key beneath it, and never the
// key at it. The root's is the namespace, which is every key of the mount.
func (f *FS) prefix(name string) string {
	return f.under + keyspace.Prefix(name)
}

// info is what the port says about the key stored under name.
func info(name string, one Entry) vfs.FileInfo {
	return vfs.FileInfo{
		Name:    path.Base(keyspace.Relative(name)),
		Size:    one.Size,
		Mode:    one.Mode,
		ModTime: one.ModTime,
	}
}
