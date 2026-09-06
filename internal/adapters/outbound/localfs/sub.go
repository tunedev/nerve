package localfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// AsSubber reports that a host directory can hand out one of its own
// directories as a root. It is the accessor that turns a subtree into a
// boundary rather than a prefix: the kernel resolves a symlink inside the
// syscall, long after any name was derived, so the only thing that refuses one
// leaving a subtree is a root opened on that subtree.
func (f *FS) AsSubber() vfs.Subber { return f }

// Sub returns the directory at name as a filesystem whose root it is. Nothing
// is opened here and nothing can fail: a subtree that is not there is a subtree
// whose verbs report fs.ErrNotExist, which is what they would have reported
// through the mount.
func (f *FS) Sub(name string) vfs.FileSystem { return &subFS{parent: f, at: path.Join("/", name)} }

// subFS is one directory of a mount, standing as its own root. It holds the
// mount and the directory's name rather than a handle on the directory, and
// opens one per verb.
//
// That is the point rather than an oversight. A subtree is a value a caller
// takes per request and may drop or may keep, so a handle held here would be a
// descriptor leaked per request or a root still pointing at a directory that
// has since been deleted and made again. A handle taken per verb is neither,
// and costs one openat beside the syscall the verb was going to make anyway.
type subFS struct {
	parent *FS
	at     string
}

// under opens the subtree as a mount of its own and hands it to act, closing
// the handle when act returns.
func under[T any](s *subFS, act func(*FS) (T, error)) (T, error) {
	opened, err := s.parent.root.OpenRoot(rel(s.at))
	if err != nil {
		var zero T
		return zero, fmt.Errorf("open the subtree: %w", sentinel(err))
	}
	defer func() { _ = opened.Close() }()
	return act(&FS{root: opened})
}

// on is under for a verb that returns nothing but an error.
func (s *subFS) on(act func(*FS) error) error {
	_, err := under(s, func(f *FS) (struct{}, error) { return struct{}{}, act(f) })
	return err
}

func (s *subFS) Create(ctx context.Context, name string) error {
	return s.on(func(f *FS) error { return f.Create(ctx, name) })
}

// Mkdir makes the subtree itself when it is not there. The port has Mkdir
// create the parents on the way, and from inside a subtree the subtree is one.
func (s *subFS) Mkdir(ctx context.Context, name string) error {
	if err := s.parent.Mkdir(ctx, s.at); err != nil {
		return err
	}
	return s.on(func(f *FS) error { return f.Mkdir(ctx, name) })
}

func (s *subFS) Remove(ctx context.Context, name string) error {
	return s.on(func(f *FS) error { return f.Remove(ctx, name) })
}

// RemoveAll deletes name and everything beneath it. A subtree that is not there
// holds nothing, which is the answer the port promises for a path that is not
// there either.
func (s *subFS) RemoveAll(ctx context.Context, name string) error {
	err := s.on(func(f *FS) error { return f.RemoveAll(ctx, name) })
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *subFS) Read(ctx context.Context, name string) ([]byte, error) {
	return under(s, func(f *FS) ([]byte, error) { return f.Read(ctx, name) })
}

func (s *subFS) Write(ctx context.Context, name string, data []byte) error {
	return s.on(func(f *FS) error { return f.Write(ctx, name, data) })
}

func (s *subFS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	return under(s, func(f *FS) ([]vfs.FileInfo, error) { return f.ReadDir(ctx, name) })
}

func (s *subFS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	return under(s, func(f *FS) (vfs.FileInfo, error) { return f.Stat(ctx, name) })
}

func (s *subFS) Rename(ctx context.Context, oldPath, newPath string) error {
	return s.on(func(f *FS) error { return f.Rename(ctx, oldPath, newPath) })
}

func (s *subFS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	return s.on(func(f *FS) error { return f.Chmod(ctx, name, mode) })
}

// Capabilities is the mount's: a directory inside a host directory is a plain
// read-write tree at every path, the same as the one holding it.
func (s *subFS) Capabilities(string) vfs.Caps { return vfs.Caps{} }

// A subtree is a host directory, so it answers every verb set the mount does.
func (s *subFS) AsSymlinker() vfs.Symlinker { return s }
func (s *subFS) AsStreamer() vfs.Streamer   { return s }
func (s *subFS) AsTruncater() vfs.Truncater { return s }
func (s *subFS) AsSubber() vfs.Subber       { return s }

// Sub narrows again, against the same mount: a subtree of a subtree is one
// directory named from the mount's root, and opening it is still one openat.
func (s *subFS) Sub(name string) vfs.FileSystem {
	return &subFS{parent: s.parent, at: path.Join(s.at, path.Join("/", name))}
}

func (s *subFS) Symlink(ctx context.Context, oldPath, newPath string) error {
	return s.on(func(f *FS) error { return f.Symlink(ctx, oldPath, newPath) })
}

func (s *subFS) Readlink(ctx context.Context, name string) (string, error) {
	return under(s, func(f *FS) (string, error) { return f.Readlink(ctx, name) })
}

func (s *subFS) Lstat(ctx context.Context, name string) (vfs.FileInfo, error) {
	return under(s, func(f *FS) (vfs.FileInfo, error) { return f.Lstat(ctx, name) })
}

func (s *subFS) Truncate(ctx context.Context, name string, size int64) error {
	return s.on(func(f *FS) error { return f.Truncate(ctx, name, size) })
}

// OpenRead hands back the open file rather than the subtree: a descriptor on a
// file outlives the root it was resolved through.
func (s *subFS) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	return under(s, func(f *FS) (io.ReadCloser, error) { return f.OpenRead(ctx, name) })
}

// OpenWrite keeps the subtree open until the stream closes, because that is
// when the write lands: the temporary file and the entry it moves onto are both
// named against the root the stream was opened on.
func (s *subFS) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	opened, err := s.parent.root.OpenRoot(rel(s.at))
	if err != nil {
		return nil, fmt.Errorf("open_write %q: %w", name, sentinel(err))
	}
	stream, err := (&FS{root: opened}).OpenWrite(ctx, name)
	if err != nil {
		_ = opened.Close()
		return nil, err
	}
	return holding{WriteCloser: stream, root: opened}, nil
}

// holding closes the subtree handle when the stream opened on it closes.
type holding struct {
	io.WriteCloser
	root *os.Root
}

func (h holding) Close() error {
	err := h.WriteCloser.Close()
	_ = h.root.Close()
	return err
}
