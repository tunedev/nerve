package fusefs

import (
	"context"
	iofs "io/fs"
	"path"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// tree is the one thing every node shares: the filesystem the mount stands
// over. It is a struct rather than the bare port so that what a node carries
// stays one word as the mount grows verbs.
type tree struct {
	fsys vfs.FileSystem
}

// node is one entry in the served tree. It holds no path: where it stands is
// the inode tree's answer, which is what keeps a rename from leaving a node
// pointing at the name it used to have.
type node struct {
	fs.Inode

	tree *tree
}

// Every verb this mount answers is declared here, so a signature that moves is
// found by the build rather than by a request quietly answering ENOSYS.
//
// Link and Symlink are absent on purpose and for different reasons. Link would
// give one entry two paths, which is a second spelling of a location and what
// workspace.Scope.Path exists to prevent. Symlink is absent because the port
// cannot describe one: Stat follows a link and reports the entry it stands for,
// so a mount that made a link would then have to tell the kernel the link is a
// regular file, and the kernel refuses that mismatch. A link planted behind the
// mount is served as the entry it stands for.
var (
	_ fs.NodeLookuper  = (*node)(nil)
	_ fs.NodeGetattrer = (*node)(nil)
	_ fs.NodeSetattrer = (*node)(nil)
	_ fs.NodeReaddirer = (*node)(nil)
	_ fs.NodeMkdirer   = (*node)(nil)
	_ fs.NodeCreater   = (*node)(nil)
	_ fs.NodeUnlinker  = (*node)(nil)
	_ fs.NodeRmdirer   = (*node)(nil)
	_ fs.NodeRenamer   = (*node)(nil)
	_ fs.NodeOpener    = (*node)(nil)
	_ fs.NodeReader    = (*node)(nil)
	_ fs.NodeWriter    = (*node)(nil)
	_ fs.NodeFlusher   = (*node)(nil)
	_ fs.NodeFsyncer   = (*node)(nil)
)

// at is the path this node stands at, in the tree's vocabulary. The inode
// hierarchy is where it comes from, so a node moved by Rename answers with the
// name it was moved to.
func (n *node) at() string { return "/" + n.Path(n.Root()) }

// child is the path of a name directly under this node.
func (n *node) child(name string) string { return path.Join(n.at(), name) }

// Lookup answers what stands at one name under this directory. The kernel asks
// for a single component and never for a path: it resolved every separator and
// every ".." against its own dentry tree before this was called.
func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return n.describe(ctx, name, out)
}

// describe answers with the inode for a name under this directory and fills
// what the kernel is told about it. Creation verbs in the port answer with an
// error and nothing else, so what an entry became is the same question after
// making one as before looking one up.
func (n *node) describe(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	info, err := n.tree.fsys.Stat(ctx, n.child(name))
	if err != nil {
		return nil, errnoOf(err)
	}
	setAttr(info, &out.Attr)
	return n.NewInode(ctx, &node{tree: n.tree}, fs.StableAttr{Mode: kind(info.Mode)}), 0
}

// Getattr describes this node from what the port says about the path, not from
// what an open descriptor is holding: an unflushed write has not happened to
// the path yet, and saying otherwise would report a length nothing can read.
func (n *node) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, err := n.tree.fsys.Stat(ctx, n.at())
	if err != nil {
		return errnoOf(err)
	}
	setAttr(info, &out.Attr)
	return 0
}

// Setattr sets what the port has verbs for and accepts the rest without doing
// it. Owner and times are the rest: no storage kind here has an owner, and a
// modification time is what a write leaves behind rather than something a
// caller sets. Refusing them would fail the ordinary copy that asks.
//
// A size is where O_TRUNC arrives. go-fuse does not negotiate ATOMIC_O_TRUNC,
// so the kernel opens the descriptor and then truncates through a request that
// carries no file handle at all: the path is what has to be cut, and any copy a
// descriptor is holding is caught up separately.
func (n *node) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	at := n.at()
	if mode, set := in.GetMode(); set {
		if err := n.tree.fsys.Chmod(ctx, at, iofs.FileMode(mode).Perm()); err != nil {
			return errnoOf(err)
		}
	}
	if size, set := in.GetSize(); set {
		if err := n.tree.truncate(ctx, at, int64(size)); err != nil {
			return errnoOf(err)
		}
		if open, ours := f.(*handle); ours {
			open.resize(int64(size))
		}
	}
	return n.Getattr(ctx, f, out)
}
