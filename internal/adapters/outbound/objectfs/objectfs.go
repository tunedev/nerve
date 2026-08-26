// Package objectfs mounts an object store as part of the tree. Locally that
// store is MinIO; hosted it is whatever speaks the same wire.
//
// It claims ObjectStore at every path, and the descriptor is the whole of what
// separates it from a tree: names are prefixes, no directory exists apart from
// the entries under it, and Mkdir has nothing to make. Callers reach it through
// the same verb set as every other kind, so nothing above has to know which of
// the two it is holding — only what it says about itself.
//
// The store itself sits behind Bucket, one port, so the plugin's semantics are
// asserted without a container and the choice between MinIO and a hosted store
// stays configuration.
package objectfs

import (
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirMode and fileMode are what an entry reads back as. A prefix is reported as
// a directory so a caller listing one sees the shape it expects; nothing is
// stored to make that true. fileMode is what an object gets when it is stored
// without a mode of its own, since mode is not a parameter of creation anywhere
// in the port.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// separator is the delimiter that makes a flat namespace read as one level of a
// tree, and the one every key uses between its parts.
const separator = "/"

// FS is one bucket seen as part of the tree.
//
// It holds no state of its own beyond the bucket: an object store is the system
// of record, so there is nothing here to keep consistent and nothing to lock.
type FS struct {
	vfs.NoAccessors

	bucket Bucket
}

// New returns a filesystem over bucket.
func New(bucket Bucket) *FS { return &FS{bucket: bucket} }

// Capabilities reports an object store, which is what this is at every path.
// Nothing is fetched to answer: the descriptor is the kind, not the entry.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{ObjectStore: true} }

// key turns a path into the flat name a bucket knows it by, resolving it
// lexically so a caller is not made to clean paths a mount table would have
// cleaned. The root is the bucket itself and has no key.
func key(name string) string {
	clean := path.Clean(separator + strings.TrimPrefix(name, separator))
	if clean == separator {
		return ""
	}
	return clean[1:]
}

// prefixOf is the key a listing walks to find what sits under name. The root
// lists the whole bucket, so its prefix is empty rather than a bare separator.
func prefixOf(name string) string {
	if k := key(name); k != "" {
		return k + separator
	}
	return ""
}

// isRoot reports whether name is the bucket itself, which no verb creates,
// moves or deletes.
func isRoot(name string) bool { return key(name) == "" }

// info is what the port says about an object.
func (o Object) info() vfs.FileInfo {
	return vfs.FileInfo{
		Name:    base(o.Key),
		Size:    o.Size,
		Mode:    o.Mode.Perm(),
		ModTime: o.ModTime,
	}
}

// dirInfo is what the port says about a prefix: a directory that exists only
// because something is stored under it, and that nothing was stored to make.
func dirInfo(prefix string) vfs.FileInfo {
	return vfs.FileInfo{Name: base(prefix), Size: 0, Mode: dirMode}
}

// base is the last element of a key, a prefix or a path, which is the name an
// entry carries however it was reached.
func base(name string) string { return path.Base(strings.TrimSuffix(name, separator)) }
