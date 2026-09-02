package fusefs

import (
	"context"
	"errors"
	iofs "io/fs"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// errnoOf is the mapper at the boundary: the port's sentinels are what a plugin
// promises to speak, and an errno is what the kernel understands. Anything with
// no name on either side is EIO, because a caller inside the mount is a program
// reading a file and has nowhere to put a Go error.
//
// A deadline that expired is the mount's own bound arriving rather than the
// storage answering, so it is reported as one.
func errnoOf(err error) syscall.Errno {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, iofs.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, iofs.ErrExist):
		return syscall.EEXIST
	case errors.Is(err, iofs.ErrPermission):
		return syscall.EACCES
	case errors.Is(err, iofs.ErrInvalid):
		return syscall.EINVAL
	case errors.Is(err, context.DeadlineExceeded):
		return syscall.ETIMEDOUT
	case errors.Is(err, context.Canceled):
		return syscall.EINTR
	}
	return syscall.EIO
}

// kind is the type bits of an entry, which is what a StableAttr carries and
// what the kernel decides how to treat a node by.
//
// A mount has two kinds, a directory and a file, and a link is not a third.
// Stat follows a link and describes what it stands for, so an entry the kernel
// was told is a link would be described as a regular file the next time it was
// looked up, and the kernel refuses that mismatch. Reporting the file is what
// the port can back.
func kind(mode iofs.FileMode) uint32 {
	if mode.IsDir() {
		return syscall.S_IFDIR
	}
	return syscall.S_IFREG
}

// setAttr fills what the kernel is told about an entry from what the port says
// about it. Nlink is one because Link is refused: a path here is the only name
// its content has, which is the property workspace.Scope.Path exists to keep.
func setAttr(info vfs.FileInfo, out *fuse.Attr) {
	out.Mode = kind(info.Mode) | uint32(info.Mode.Perm())
	out.Size = uint64(info.Size)
	out.Nlink = 1
	out.SetTimes(nil, &info.ModTime, &info.ModTime)
}
