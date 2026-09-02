package fusefs

import (
	"context"
	"errors"
	iofs "io/fs"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Readdir lists this directory. The stream is built whole rather than lazily,
// because ReadDir is one call in the port and there is nothing to hold open
// between entries.
func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	children, err := n.tree.fsys.ReadDir(ctx, n.at())
	if err != nil {
		return nil, errnoOf(err)
	}

	entries := make([]fuse.DirEntry, 0, len(children))
	for _, child := range children {
		entries = append(entries, fuse.DirEntry{Name: child.Name, Mode: kind(child.Mode)})
	}
	return fs.NewListDirStream(entries), 0
}

// Mkdir makes a directory. The mode the caller asked for is not passed on:
// mode is not a parameter of creation anywhere in the port, because most
// storage kinds have none and would have to invent one to be created. A caller
// that wants different bits chmods, which arrives as Setattr.
func (n *node) Mkdir(ctx context.Context, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if err := n.tree.fsys.Mkdir(ctx, n.child(name)); err != nil {
		return nil, errnoOf(err)
	}
	return n.describe(ctx, name, out)
}

// Create makes an empty entry and opens it. This is the one open whose copy is
// taken here rather than at the first read: the entry was just made and is
// empty, so reading it back would be a round trip for a known answer.
func (n *node) Create(
	ctx context.Context, name string, _, _ uint32, out *fuse.EntryOut,
) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if err := n.tree.fsys.Create(ctx, n.child(name)); err != nil {
		return nil, nil, 0, errnoOf(err)
	}
	child, errno := n.describe(ctx, name, out)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	return child, &handle{loaded: true}, 0, 0
}

// Unlink deletes a name under this directory.
func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	return errnoOf(n.tree.fsys.Remove(ctx, n.child(name)))
}

// Rmdir deletes a directory under this one. The port spells "there are still
// entries in it" as fs.ErrExist, and rmdir(2) spells the same condition
// ENOTEMPTY: it is the answer callers branch on, and EEXIST here would make an
// ordinary recursive delete stop.
func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	err := n.tree.fsys.Remove(ctx, n.child(name))
	if errors.Is(err, iofs.ErrExist) {
		return syscall.ENOTEMPTY
	}
	return errnoOf(err)
}

// Rename moves a name to another directory in the same mount. Both ends are
// paths in the tree, so the destination's is asked of the node standing there
// rather than assembled here.
//
// RENAME_EXCHANGE and RENAME_NOREPLACE are refused: the port has one Rename and
// it replaces, so honouring either would mean a lookup and a second call with
// the answer already stale. A caller that gets ENOTSUP does it the ordinary way.
func (n *node) Rename(
	ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32,
) syscall.Errno {
	if flags != 0 {
		return syscall.ENOTSUP
	}
	into, ours := newParent.(*node)
	if !ours {
		return syscall.EXDEV
	}
	return errnoOf(n.tree.fsys.Rename(ctx, n.child(name), into.child(newName)))
}
