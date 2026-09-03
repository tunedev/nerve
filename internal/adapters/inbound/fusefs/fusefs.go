// Package fusefs serves a vfs.FileSystem to the kernel, so that a process with
// no knowledge of Nerve sees an ordinary directory tree. It is an inbound
// adapter: the kernel is the caller, and every FUSE request lands here and
// leaves as a port verb.
//
// What it is handed is the port and nothing wider. A workspace hands out
// workspace.Scope.View, so containment stays where E5.1 put it and the mount
// derives no path of its own; a mount over the tree directly is the same
// adapter with no scope in front of it.
//
// FUSE is a data boundary and not a process boundary. A path spelling ".."
// never reaches a filesystem — the kernel resolves it against its own dentry
// tree — so what this package can refuse is what it is asked for, not what the
// caller can address. Confining a process to the mount is a mount namespace,
// which is somebody else's verb.
//
// The tree has two kinds of entry, a directory and a file. Neither sort of link
// is served: a hard link would give one entry two paths, and a symbolic link is
// something the port cannot describe, because Stat follows one and reports what
// it stands for. A link planted behind the mount is therefore served as the
// entry it stands for, and creating one through the mount is refused.
package fusefs

import (
	"errors"
	"fmt"
	"os"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// name is the "fuse.<name>" the mount reports itself as to df and mountinfo.
const name = "nerve"

// Serve mounts fsys at dir and answers the kernel until Unmount. dir must
// already exist and be a directory, which is a host path and not a path in the
// tree: what can read it is inside the workspace.
//
// The mount is uid-scoped, which is the kernel's default for an unprivileged
// FUSE mount and the only setting here that is a boundary. AllowOther would
// widen it to every user on the host and is never set.
//
// Nothing is cached: passing any Options at all leaves both the entry and the
// attribute timeout nil, so the kernel re-asks for everything. That is slow and
// it is the safe direction — a mount that hands out a timeout hands out a
// window in which a revoked path still answers.
//
// A mount left at dir by a process that died holding one is cleared first. That
// is the whole of what a killed process leaves behind, and mounting is where it
// is reached: the point is unusable until something unmounts it, and the next
// mount is the only thing that comes for it.
func Serve(dir string, fsys vfs.FileSystem) (*Mount, error) {
	if err := reclaim(dir); err != nil {
		return nil, err
	}

	root := &node{tree: &tree{fsys: fsys}}
	server, err := fs.Mount(dir, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:   name,
			FsName: name,
			// The port has no vocabulary for extended attributes, so
			// ENOSYS is the honest answer and this stops the kernel
			// asking for them at all.
			DisableXAttrs: true,
		},
		// Entries belong to the process serving them. Left unset they
		// read as root's, which is a claim this mount cannot back.
		UID: uint32(os.Getuid()),
		GID: uint32(os.Getgid()),
	})
	if err != nil {
		return nil, fmt.Errorf("mount %q: %w", dir, err)
	}
	return &Mount{server: server, dir: dir}, nil
}

// Mount is one served filesystem. It is what a caller unmounts and waits on.
type Mount struct {
	server *fuse.Server
	dir    string
}

// Unmount takes the filesystem out of the mount table, and leaves nothing there
// when it returns nil.
//
// The ordinary unmount fails with EBUSY while any descriptor opened through the
// mount is still open, which at shutdown is a mount left behind rather than
// something a caller can wait out: whatever holds the descriptor is inside, and
// the process about to exit is what serves it. The fallback is a lazy detach —
// the mount leaves the table at once and the kernel frees it when the last
// descriptor closes.
//
// Wait does not return after a detach until those descriptors are closed, so a
// caller that must exit should not wait on one.
func (m *Mount) Unmount() error {
	first := m.server.Unmount()
	if first == nil {
		return nil
	}
	if second := detach(m.dir); second != nil {
		return fmt.Errorf("unmount %q: %w", m.dir, errors.Join(first, second))
	}
	return nil
}

// Wait blocks until the filesystem is unmounted, by Unmount or from outside.
func (m *Mount) Wait() { m.server.Wait() }
