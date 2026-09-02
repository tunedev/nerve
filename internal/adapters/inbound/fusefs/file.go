package fusefs

import (
	"context"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// handle is one open file. The port moves whole contents, so a descriptor
// keeps the entry in memory and every offset is served from that copy; a write
// lands in it and reaches the plugin when the descriptor is flushed.
//
// The copy is taken at the first read or write and not at the open, because
// the kernel opens a descriptor before it truncates one: an O_TRUNC open
// arrives as OPEN and then a bare SETATTR carrying no file handle, so a copy
// taken at the open is a copy of content that is already gone by the time
// anything reads it.
//
// What this costs is the entry's length in memory per open descriptor. What it
// buys is that every storage kind behaves the same through a mount, including
// the ones with no notion of an offset at all. A kind whose entries outgrow
// memory wants AsStreamer, and reaching for it is a later ticket than making
// the tree real.
//
// Two descriptors on one path each hold their own copy, so what one writes the
// other sees after a flush and not before. That is close-to-open, which is what
// every network filesystem promises and what a mount over remote storage can
// promise honestly.
type handle struct {
	mu     sync.Mutex
	loaded bool
	data   []byte
	dirty  bool
}

// resize cuts or zero-pads a copy that has already been taken, so a truncate
// through a descriptor is what its next read sees. One that has not been taken
// is left alone: the path behind it was truncated, and the copy this descriptor
// eventually takes is of what is there then.
func (h *handle) resize(size int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.loaded {
		return
	}
	h.data = resized(h.data, size)
	h.dirty = true
}

// resized returns data cut or zero-padded to size, which is what Truncate
// promises.
func resized(data []byte, size int64) []byte {
	if size <= int64(len(data)) {
		return data[:size]
	}
	return append(data, make([]byte, size-int64(len(data)))...)
}

// truncate cuts a path to size. A storage kind with the verb is asked for it;
// one without is read and written back, which is the only spelling the port's
// whole-content verbs leave.
func (t *tree) truncate(ctx context.Context, at string, size int64) error {
	if cut := t.fsys.AsTruncater(); cut != nil {
		return cut.Truncate(ctx, at, size)
	}
	data, err := t.fsys.Read(ctx, at)
	if err != nil {
		return err
	}
	return t.fsys.Write(ctx, at, resized(data, size))
}

// load reads the entry into the open copy, once. The caller holds the lock.
//
// On a path whose Caps say reading consumes, this is the read that consumes it:
// one descriptor takes one entry, which is what a queue means by a file.
func (n *node) load(ctx context.Context, open *handle) error {
	if open.loaded {
		return nil
	}
	data, err := n.tree.fsys.Read(ctx, n.at())
	if err != nil {
		return err
	}
	open.data, open.loaded = data, true
	return nil
}

// held is the open copy behind a file handle. A request carrying one the mount
// did not hand out is a bad descriptor.
func held(f fs.FileHandle) (*handle, syscall.Errno) {
	open, ours := f.(*handle)
	if !ours {
		return nil, syscall.EBADF
	}
	return open, 0
}

// Open hands back the copy every later offset is served from. Nothing is read
// here: see handle.
func (n *node) Open(_ context.Context, _ uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return &handle{}, 0, 0
}

// Read serves an offset out of the open copy, into the buffer the kernel
// supplied. An offset past the end is not an error: read(2) answers zero bytes
// there.
func (n *node) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	open, errno := held(f)
	if errno != 0 {
		return nil, errno
	}

	open.mu.Lock()
	defer open.mu.Unlock()

	if err := n.load(ctx, open); err != nil {
		return nil, errnoOf(err)
	}
	if off >= int64(len(open.data)) {
		return fuse.ReadResultData(nil), 0
	}
	return fuse.ReadResultData(dest[:copy(dest, open.data[off:])]), 0
}

// Write puts data at an offset in the open copy, growing it to reach one past
// the end. Nothing reaches the plugin here: the whole content is what the port
// writes, and the descriptor is not finished yet.
func (n *node) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	open, errno := held(f)
	if errno != 0 {
		return 0, errno
	}

	open.mu.Lock()
	defer open.mu.Unlock()

	if err := n.load(ctx, open); err != nil {
		return 0, errnoOf(err)
	}
	if end := off + int64(len(data)); end > int64(len(open.data)) {
		open.data = resized(open.data, end)
	}
	copy(open.data[off:], data)
	open.dirty = true
	return uint32(len(data)), 0
}

// Flush writes the open copy back. It runs on close(2), which a descriptor
// duplicated with dup(2) reaches more than once, so it clears the flag rather
// than writing the same content again.
//
// This is where a write fails. A caller that never checks the result of close
// never learns that its write did not land, which is true of every filesystem
// and is why Fsync answers the same way.
func (n *node) Flush(ctx context.Context, f fs.FileHandle) syscall.Errno {
	open, errno := held(f)
	if errno != 0 {
		return errno
	}

	open.mu.Lock()
	defer open.mu.Unlock()

	if !open.dirty {
		return 0
	}
	if err := n.tree.fsys.Write(ctx, n.at(), open.data); err != nil {
		return errnoOf(err)
	}
	open.dirty = false
	return 0
}

// Fsync writes the open copy back, which is what Flush does: there is no layer
// between the copy and the plugin to push through.
func (n *node) Fsync(ctx context.Context, f fs.FileHandle, _ uint32) syscall.Errno {
	return n.Flush(ctx, f)
}
