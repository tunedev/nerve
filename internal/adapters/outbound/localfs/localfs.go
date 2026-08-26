// Package localfs passes the port through to a directory on the host disk. It
// is the plugin that stores nothing itself: the tree it answers for is whatever
// is already in that directory, and what another process writes there is what a
// caller reads back.
//
// It claims no descriptor at any path, and refuses no accessor: links, streams
// and truncation are native to a disk, so this is where the optional verb sets
// memfs invented nothing for are actually answered.
//
// A path is resolved lexically before it is joined to the host directory, so no
// spelling of it reaches outside the mount: "/.." is the root, the same way it
// is in a tree that has no host under it.
package localfs

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirPerm and filePerm are what an entry gets when it is made, before the
// host's umask has its say. Mode is not a parameter of creation anywhere in the
// port, so Chmod is the only way to change either.
const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// FS is one host directory answering the port. It holds no state beyond where
// that directory is: the host filesystem is the tree, and it does its own
// locking, so there is nothing here for a mutex to guard.
type FS struct {
	root string
}

// New returns a filesystem over an existing host directory, named by an
// absolute path.
func New(root string) *FS {
	return &FS{root: root}
}

// Capabilities reports a plain read-write tree, which is what a host directory
// is at every path.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{} }

// host maps a mount path onto the host path it stands for. The path is cleaned
// first, so a caller is not made to clean paths a mount table would have
// cleaned, and so nothing under the root can name anything above it.
func (f *FS) host(name string) string {
	return filepath.Join(f.root, filepath.FromSlash(path.Join("/", name)))
}

// mount maps a host path back into the mount's vocabulary, which is what a link
// target has to be read back in. A target outside the root is handed back as
// the host wrote it: the mount has no name for it.
func (f *FS) mount(host string) string {
	rel, err := filepath.Rel(f.root, host)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return host
	}
	return path.Join("/", filepath.ToSlash(rel))
}

// elements splits a mount path into the names below the root. The root itself
// has no elements, and neither has a path that climbs past it.
func elements(name string) []string {
	clean := path.Join("/", name)
	if clean == "/" {
		return nil
	}
	return strings.Split(clean[1:], "/")
}

// isRoot reports whether name is the ground the mount stands on, which Remove
// and Rename are held to refuse.
func isRoot(name string) bool { return len(elements(name)) == 0 }

// entryName is the final element of a path, which is what the port reports as a
// name. It comes from the path the caller asked for rather than from the host,
// so the root of a mount is named as the mount sees it.
func entryName(name string) string {
	elems := elements(name)
	if len(elems) == 0 {
		return "/"
	}
	return elems[len(elems)-1]
}

// info is what the port says about an entry the host described. A directory has
// no content, so its size is zero however many bytes the host spends on it.
func info(name string, host fs.FileInfo) vfs.FileInfo {
	size := host.Size()
	if host.IsDir() {
		size = 0
	}
	return vfs.FileInfo{Name: entryName(name), Size: size, Mode: host.Mode(), ModTime: host.ModTime()}
}

// invalid translates the host errors that mean "not a thing you can do to this
// path" into the sentinel the port names. os already maps ENOENT, EEXIST,
// ENOTEMPTY and EACCES onto sentinels; these have no mapping of their own and
// would otherwise reach a caller as a bare errno.
func invalid(err error) error {
	if errors.Is(err, syscall.EISDIR) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("%w: %w", fs.ErrInvalid, err)
	}
	return err
}
