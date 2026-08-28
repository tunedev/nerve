package workspace

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// View returns fsys as a caller inside s sees it under root: a FileSystem whose
// own root is the scope root, and whose every path — both ends of Rename and
// Symlink included — is derived by Path.
//
// This is what makes the scope a boundary rather than a convention. A caller
// handed the tree derives its own paths and is contained only while it does;
// Symlink takes a target nothing dispatches, so a target under another user's
// subtree in the same mount is clean, is inside one mount, and is inside the
// plugin's root, and every layer beneath permits it. A caller handed a view has
// no spelling for that target: the derivation is the view's, and a path that
// climbs lands on the scope root.
//
// root is the area the view is over — the files mount, the key space — and must
// be a rooted, clean path, the mount table's rule for a prefix.
func (s Scope) View(fsys vfs.FileSystem, root string) (vfs.FileSystem, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("view root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	if _, err := s.Path(root, ""); err != nil {
		return nil, fmt.Errorf("view: %w", err)
	}
	return view{fsys: fsys, scope: s, root: root}, nil
}

// view is one area of one scope, seen from inside it. It stores nothing and
// resolves nothing: every verb derives the path the scope stands for and hands
// it on.
type view struct {
	fsys  vfs.FileSystem
	scope Scope
	root  string
}

// at is the path name stands for beneath the view. It is Path and nothing else,
// which is the whole property: a second way to spell the layout here is a way
// to spell it differently.
func (v view) at(name string) (string, error) {
	return v.scope.Path(v.root, name)
}

// from maps a path the tree beneath spelled back into the view's vocabulary,
// which is what Readlink has to answer in. One outside the scope is refused
// rather than handed back: a path is observable even when what stands at it is
// not.
func (v view) from(name string) (string, error) {
	base, err := v.at("")
	if err != nil {
		return "", err
	}
	clean := path.Clean(name)
	if clean == base {
		return "/", nil
	}
	if inside, found := strings.CutPrefix(clean, base+"/"); found {
		return "/" + inside, nil
	}
	return "", fmt.Errorf("readlink %q: it stands for a path outside the scope: %w", name, fs.ErrInvalid)
}

// isRoot reports whether name is the scope root, which is the ground the view
// stands on.
func isRoot(name string) bool { return path.Join("/", name) == "/" }

// refuseRoot is what the two verbs that would take the ground away answer with.
func (v view) refuseRoot(verb string) error {
	return fmt.Errorf("%s the root of a view of %+v: %w", verb, v.scope, fs.ErrInvalid)
}

func (v view) Create(ctx context.Context, name string) error {
	inside, err := v.at(name)
	if err != nil {
		return err
	}
	return v.fsys.Create(ctx, inside)
}

func (v view) Mkdir(ctx context.Context, name string) error {
	inside, err := v.at(name)
	if err != nil {
		return err
	}
	return v.fsys.Mkdir(ctx, inside)
}

// Remove refuses the root: a view that deleted the scope it stands for would
// answer nothing afterwards.
func (v view) Remove(ctx context.Context, name string) error {
	if isRoot(name) {
		return v.refuseRoot("remove")
	}
	inside, err := v.at(name)
	if err != nil {
		return err
	}
	return v.fsys.Remove(ctx, inside)
}

// RemoveAll empties the root rather than deleting it, for the reason Remove
// refuses one.
func (v view) RemoveAll(ctx context.Context, name string) error {
	inside, err := v.at(name)
	if err != nil {
		return err
	}
	if !isRoot(name) {
		return v.fsys.RemoveAll(ctx, inside)
	}

	children, err := v.fsys.ReadDir(ctx, inside)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := v.fsys.RemoveAll(ctx, path.Join(inside, child.Name)); err != nil {
			return err
		}
	}
	return nil
}

func (v view) Read(ctx context.Context, name string) ([]byte, error) {
	inside, err := v.at(name)
	if err != nil {
		return nil, err
	}
	return v.fsys.Read(ctx, inside)
}

func (v view) Write(ctx context.Context, name string, data []byte) error {
	inside, err := v.at(name)
	if err != nil {
		return err
	}
	return v.fsys.Write(ctx, inside, data)
}

// ReadDir names the children as the tree beneath named them: a name is the
// final element of a path, and deriving the path did not change that.
func (v view) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	inside, err := v.at(name)
	if err != nil {
		return nil, err
	}
	return v.fsys.ReadDir(ctx, inside)
}

// Stat names the root "/" rather than the workspace: what the scope is called
// is the layout speaking, and a caller inside a view is not told the layout.
func (v view) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	inside, err := v.at(name)
	if err != nil {
		return vfs.FileInfo{}, err
	}
	info, err := v.fsys.Stat(ctx, inside)
	if err == nil && isRoot(name) {
		info.Name = "/"
	}
	return info, err
}

// Rename derives both ends, and refuses to move the root for the reason Remove
// refuses to delete it.
func (v view) Rename(ctx context.Context, oldPath, newPath string) error {
	if isRoot(oldPath) || isRoot(newPath) {
		return v.refuseRoot("rename")
	}
	oldInside, err := v.at(oldPath)
	if err != nil {
		return err
	}
	newInside, err := v.at(newPath)
	if err != nil {
		return err
	}
	return v.fsys.Rename(ctx, oldInside, newInside)
}

func (v view) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	inside, err := v.at(name)
	if err != nil {
		return err
	}
	return v.fsys.Chmod(ctx, inside, mode)
}

// Capabilities forwards, per path: what a view is depends entirely on what
// stands behind it at the path the scope derives.
func (v view) Capabilities(name string) vfs.Caps {
	inside, err := v.at(name)
	if err != nil {
		return vfs.Caps{}
	}
	return v.fsys.Capabilities(inside)
}

// AsSymlinker returns the link verbs of the tree beneath, with both of their
// paths derived, or nil when it has none. Deriving the target is the reason a
// view exists: it is the one path a caller could otherwise spell freely.
func (v view) AsSymlinker() vfs.Symlinker {
	if link := v.fsys.AsSymlinker(); link != nil {
		return viewSymlinker{view: v, link: link}
	}
	return nil
}

// AsStreamer returns the streaming verbs of the tree beneath, or nil when it
// has none.
func (v view) AsStreamer() vfs.Streamer {
	if stream := v.fsys.AsStreamer(); stream != nil {
		return viewStreamer{view: v, stream: stream}
	}
	return nil
}

// AsTruncater returns the truncate verb of the tree beneath, or nil when it has
// none.
func (v view) AsTruncater() vfs.Truncater {
	if cut := v.fsys.AsTruncater(); cut != nil {
		return viewTruncater{view: v, cut: cut}
	}
	return nil
}

type viewSymlinker struct {
	view view
	link vfs.Symlinker
}

// Symlink derives both paths. The target is a path like any other here, so a
// spelling that climbs lands on the scope root and one naming another scope
// lands inside this one.
func (s viewSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	target, err := s.view.at(oldPath)
	if err != nil {
		return err
	}
	link, err := s.view.at(newPath)
	if err != nil {
		return err
	}
	return s.link.Symlink(ctx, target, link)
}

// Readlink answers in the view's vocabulary. A link planted behind the view
// that stands for a path outside the scope has no name here and is refused.
func (s viewSymlinker) Readlink(ctx context.Context, name string) (string, error) {
	inside, err := s.view.at(name)
	if err != nil {
		return "", err
	}
	target, err := s.link.Readlink(ctx, inside)
	if err != nil {
		return "", err
	}
	return s.view.from(target)
}

type viewStreamer struct {
	view   view
	stream vfs.Streamer
}

func (s viewStreamer) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	inside, err := s.view.at(name)
	if err != nil {
		return nil, err
	}
	return s.stream.OpenRead(ctx, inside)
}

func (s viewStreamer) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	inside, err := s.view.at(name)
	if err != nil {
		return nil, err
	}
	return s.stream.OpenWrite(ctx, inside)
}

type viewTruncater struct {
	view view
	cut  vfs.Truncater
}

func (c viewTruncater) Truncate(ctx context.Context, name string, size int64) error {
	inside, err := c.view.at(name)
	if err != nil {
		return err
	}
	return c.cut.Truncate(ctx, inside, size)
}
