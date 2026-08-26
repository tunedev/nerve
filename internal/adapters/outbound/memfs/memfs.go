// Package memfs keeps a whole tree in memory. It is the reference plugin: the
// one every other storage kind is read against, and the substrate tests mount
// when what they exercise is the tree rather than the storage under it.
//
// It claims no descriptor at any path. A plugin that does claim one is read
// against a tree where none is set, so what the descriptor changes is visible
// rather than assumed.
package memfs

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirMode and fileMode are what an entry gets when it is made. Mode is not a
// parameter of creation anywhere in the port, so Chmod is the only way to
// change either.
const (
	dirMode  = fs.ModeDir | 0o755
	fileMode = 0o644
)

// FS is one in-memory tree, safe for concurrent use.
//
// One mutex covers the whole tree rather than one per node: a rename moves an
// entry between two parents and a walk reads every node on the way down, so
// per-node locking would have to be taken in an order neither operation knows
// before it starts.
type FS struct {
	vfs.NoAccessors

	mu   sync.RWMutex
	root *node
}

// node is one entry. A directory has children and no data; a file has data and
// no children.
type node struct {
	name     string
	mode     fs.FileMode
	modTime  time.Time
	data     []byte
	children map[string]*node
}

// New returns a tree holding nothing but its root directory.
func New() *FS {
	return &FS{root: newDir("/")}
}

// Capabilities reports a plain read-write tree, which is what memfs is at every
// path.
func (f *FS) Capabilities(string) vfs.Caps { return vfs.Caps{} }

func newDir(name string) *node {
	return &node{name: name, mode: dirMode, modTime: time.Now(), children: make(map[string]*node)}
}

func (n *node) isDir() bool { return n.mode.IsDir() }

// info is what the port says about an entry.
func (n *node) info() vfs.FileInfo {
	size := int64(len(n.data))
	if n.isDir() {
		size = 0
	}
	return vfs.FileInfo{Name: n.name, Size: size, Mode: n.mode, ModTime: n.modTime}
}

// elements splits a path into the names below the root, resolving it lexically
// so a caller is not made to clean paths a mount table would have cleaned. The
// root itself has no elements, and neither has a path that climbs past it.
func elements(name string) []string {
	clean := path.Clean("/" + strings.TrimPrefix(name, "/"))
	if clean == "/" {
		return nil
	}
	return strings.Split(clean[1:], "/")
}

// samePath reports whether two spellings name the same entry.
func samePath(a, b string) bool {
	return path.Join(elements(a)...) == path.Join(elements(b)...)
}

// walk resolves name to the node holding it.
func (f *FS) walk(name string) (*node, error) {
	current := f.root
	for _, element := range elements(name) {
		if !current.isDir() {
			return nil, fmt.Errorf("%q: %q is not a directory: %w", name, current.name, fs.ErrInvalid)
		}
		child, exists := current.children[element]
		if !exists {
			return nil, fmt.Errorf("%q: %w", name, fs.ErrNotExist)
		}
		current = child
	}
	return current, nil
}

// parent resolves everything but the last element of name and hands back that
// last element, which is what a verb that makes, moves or removes an entry
// needs. The root is not an entry in anything, so it has no parent.
func (f *FS) parent(name string) (*node, string, error) {
	elems := elements(name)
	if len(elems) == 0 {
		return nil, "", fmt.Errorf("%q: the root: %w", name, fs.ErrInvalid)
	}

	holder, err := f.walk("/" + path.Join(elems[:len(elems)-1]...))
	if err != nil {
		return nil, "", err
	}
	if !holder.isDir() {
		return nil, "", fmt.Errorf("%q: %q is not a directory: %w", name, holder.name, fs.ErrInvalid)
	}
	return holder, elems[len(elems)-1], nil
}
