// Package localfs passes the port through to a directory on the host disk. It
// is the plugin that stores nothing itself: the tree it answers for is whatever
// is already in that directory, and what another process writes there is what a
// caller reads back.
//
// It claims no descriptor at any path, and refuses no accessor: links, streams
// and truncation are native to a disk, so this is where the optional verb sets
// memfs invented nothing for are actually answered.
//
// Containment is the operating system's, not a string comparison's. The
// directory is held open as an os.Root and every verb names a path relative to
// it, so no spelling of a path reaches outside the mount and neither does a
// symlink: the kernel resolves one at the syscall, long after any name could
// have been cleaned, and refuses it there.
package localfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// FS is one host directory answering the port. It holds an open handle on that
// directory rather than its name, because the handle is what the operating
// system enforces containment against. There is nothing else to hold: the host
// filesystem is the tree, and it does its own locking, so there is no mutex
// here either.
type FS struct {
	root *os.Root
}

// New returns a filesystem over an existing host directory, named by an
// absolute path. Opening the directory is what containment rests on, so a
// directory that will not open is an error here rather than at the first verb.
func New(root string) (*FS, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open root %q: %w", root, sentinel(err))
	}
	return &FS{root: opened}, nil
}

// Capabilities reports a plain read-write tree, which is what a host directory
// is at every path.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{} }

// rel maps a mount path onto the name an os.Root takes, which is a path below
// the root directory. The path is cleaned first, so a caller is not made to
// clean paths a mount table would have cleaned, and the root itself is the one
// name os.Root spells ".".
func rel(name string) string {
	clean := path.Join("/", name)
	if clean == "/" {
		return "."
	}
	return filepath.FromSlash(clean[1:])
}

// linkTarget maps a link target back into the mount's vocabulary, which is what
// Readlink has to answer in. A target is written relative to the directory
// holding the link, so it is resolved against that directory here. One that
// climbs out of the root, or names a host path absolutely, is handed back as
// the host wrote it: the mount has no name for it.
func linkTarget(dir, target string) string {
	if path.IsAbs(target) {
		return target
	}
	resolved := path.Join(strings.Join(elements(dir), "/"), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return target
	}
	return path.Join("/", resolved)
}

// relative spells to as a path from the directory from, which is how a link
// target is written. It is relative because os.Root follows a relative link and
// refuses an absolute one, and a link a mount made is a link that mount follows.
func relative(from, to string) string {
	base, target := elements(from), elements(to)
	shared := 0
	for shared < len(base) && shared < len(target) && base[shared] == target[shared] {
		shared++
	}

	steps := make([]string, 0, len(base)+len(target)-2*shared)
	for range base[shared:] {
		steps = append(steps, "..")
	}
	steps = append(steps, target[shared:]...)
	if len(steps) == 0 {
		return "."
	}
	return path.Join(steps...)
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

// sentinel translates the host errors the port has a name for into the name it
// uses. os already maps ENOENT, EEXIST, ENOTEMPTY and EACCES onto sentinels;
// the rest here have no mapping and would otherwise reach a caller as a bare
// errno or as an error only os can spell.
func sentinel(err error) error {
	if escaped(err) {
		return fmt.Errorf("%w: %w", fs.ErrPermission, err)
	}
	if errors.Is(err, syscall.EISDIR) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("%w: %w", fs.ErrInvalid, err)
	}
	return err
}

// escaped reports whether the host refused an operation because the path led
// out of the root. That refusal is containment doing its job, and the port
// spells it as a permission: the mount will not leave itself. os exports no
// sentinel for the condition, so its wording is the only handle; the
// containment tests assert what this becomes, so a rewording fails loudly.
func escaped(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}
