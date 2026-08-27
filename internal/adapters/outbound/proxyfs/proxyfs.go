// Package proxyfs forwards every verb to another plugin, rewriting its own root
// onto a prefix of that plugin's tree. It stores nothing: what it answers is
// whatever the plugin behind it answers, at a different name.
//
// It is where the settled decision is demonstrated rather than argued. A proxy
// is a dispatcher, so a caller holding one and reaching for an optional verb set
// with fsys.(Streamer) would interrogate the proxy and get nothing, however
// well the plugin behind it streams. The accessors on the base interface make
// the forwarding compulsory, and the descriptor is forwarded with them, per
// path: what a proxy claims at a name is what its target claims at the name
// that stands for it.
package proxyfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// FS is one forwarding mount.
type FS struct {
	target vfs.FileSystem
	prefix string
}

// New returns a mount answering for the subtree of target rooted at prefix. A
// prefix of "/" makes a plain pass-through, which is what a proxy that only
// renames nothing is.
func New(target vfs.FileSystem, prefix string) *FS {
	return &FS{target: target, prefix: path.Join("/", prefix)}
}

// Capabilities forwards, per path, because what a proxy is depends entirely on
// what stands behind it: a proxy over a queue is read-destructive and a proxy
// over a disk is not, and neither is something this can know for itself.
func (f *FS) Capabilities(name string) vfs.Caps { return f.target.Capabilities(f.at(name)) }

// Create forwards.
func (f *FS) Create(ctx context.Context, name string) error {
	return f.target.Create(ctx, f.at(name))
}

// Mkdir forwards.
func (f *FS) Mkdir(ctx context.Context, name string) error {
	return f.target.Mkdir(ctx, f.at(name))
}

// Remove forwards, except at the root: the subtree a proxy stands on is the
// ground it stands on, and removing it would leave a mount pointing at nothing.
func (f *FS) Remove(ctx context.Context, name string) error {
	if isRoot(name) {
		return f.refuseRoot("remove")
	}
	return f.target.Remove(ctx, f.at(name))
}

// RemoveAll forwards, and at the root empties the subtree rather than deleting
// it, for the reason Remove refuses one: a mount that removed its own root
// would answer nothing afterwards.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if !isRoot(name) {
		return f.target.RemoveAll(ctx, f.at(name))
	}

	children, err := f.target.ReadDir(ctx, f.prefix)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := f.target.RemoveAll(ctx, path.Join(f.prefix, child.Name)); err != nil {
			return err
		}
	}
	return nil
}

// Read forwards.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	return f.target.Read(ctx, f.at(name))
}

// Write forwards.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	return f.target.Write(ctx, f.at(name), data)
}

// ReadDir forwards, naming the children as the target named them: a name is the
// final element of a path, and rewriting the path did not change that.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	return f.target.ReadDir(ctx, f.at(name))
}

// Stat forwards, except at the root, whose name is the proxy's own rather than
// the subtree's: a caller that mounted a proxy should not have to know what it
// was pointed at to read a name back.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	info, err := f.target.Stat(ctx, f.at(name))
	if err == nil && isRoot(name) {
		info.Name = "/"
	}
	return info, err
}

// Rename forwards both paths, and refuses to move the root for the reason
// Remove refuses to delete it.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	if isRoot(oldPath) || isRoot(newPath) {
		return f.refuseRoot("rename")
	}
	return f.target.Rename(ctx, f.at(oldPath), f.at(newPath))
}

// Chmod forwards.
func (f *FS) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	return f.target.Chmod(ctx, f.at(name), mode)
}

// AsSymlinker forwards the target's link verbs, or nil when it has none. The
// answer is the target's rather than the proxy's, which is the whole reason the
// accessor is on the base interface.
func (f *FS) AsSymlinker() vfs.Symlinker {
	if link := f.target.AsSymlinker(); link != nil {
		return &symlinker{fsys: f, link: link}
	}
	return nil
}

// AsStreamer forwards the target's streaming verbs, or nil when it has none.
func (f *FS) AsStreamer() vfs.Streamer {
	if stream := f.target.AsStreamer(); stream != nil {
		return &streamer{fsys: f, stream: stream}
	}
	return nil
}

// AsTruncater forwards the target's truncate verb, or nil when it has none.
func (f *FS) AsTruncater() vfs.Truncater {
	if cut := f.target.AsTruncater(); cut != nil {
		return &truncater{fsys: f, cut: cut}
	}
	return nil
}

type symlinker struct {
	fsys *FS
	link vfs.Symlinker
}

// Symlink forwards both paths, because a link inside the subtree is written in
// the subtree's names.
func (s *symlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	return s.link.Symlink(ctx, s.fsys.at(oldPath), s.fsys.at(newPath))
}

// Readlink forwards, and maps what comes back into the proxy's names: a target
// spelled in the subtree's vocabulary would name a path the caller cannot
// reach. One pointing outside the subtree is handed back as the target wrote
// it, because the proxy has no name for it.
func (s *symlinker) Readlink(ctx context.Context, name string) (string, error) {
	target, err := s.link.Readlink(ctx, s.fsys.at(name))
	if err != nil {
		return "", err
	}
	return s.fsys.from(target), nil
}

type streamer struct {
	fsys   *FS
	stream vfs.Streamer
}

func (s *streamer) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	return s.stream.OpenRead(ctx, s.fsys.at(name))
}

func (s *streamer) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	return s.stream.OpenWrite(ctx, s.fsys.at(name))
}

type truncater struct {
	fsys *FS
	cut  vfs.Truncater
}

func (t *truncater) Truncate(ctx context.Context, name string, size int64) error {
	return t.cut.Truncate(ctx, t.fsys.at(name), size)
}

// at maps a path the proxy was given onto the path the target answers for. It
// cleans first, so a caller is not made to clean paths a mount table would have
// cleaned and no spelling reaches outside the subtree.
func (f *FS) at(name string) string {
	return path.Join(f.prefix, path.Clean("/"+strings.TrimPrefix(name, "/")))
}

// from maps a path the target spelled back into the proxy's names. A path
// outside the subtree is handed back unchanged: it is not somewhere the proxy
// can name.
func (f *FS) from(name string) string {
	if !path.IsAbs(name) {
		return name
	}
	clean := path.Clean(name)
	if f.prefix == "/" {
		return clean
	}
	if clean == f.prefix {
		return "/"
	}
	if inside, found := strings.CutPrefix(clean, f.prefix+"/"); found {
		return "/" + inside
	}
	return name
}

// isRoot reports whether name is the ground the proxy stands on.
func isRoot(name string) bool {
	return path.Clean("/"+strings.TrimPrefix(name, "/")) == "/"
}

func (f *FS) refuseRoot(verb string) error {
	return fmt.Errorf("%s the root of a proxy, which stands for %q: %w", verb, f.prefix, fs.ErrInvalid)
}
