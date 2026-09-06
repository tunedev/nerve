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
// own root is the scope root, and which has no spelling at all for a path
// outside it.
//
// This is what makes the scope a boundary rather than a convention, and it is
// two boundaries rather than one. The name is contained by derivation: every
// path — both ends of Rename and Symlink included — is resolved against the
// scope root, so a spelling that climbs lands on that root and a target under
// another user's subtree cannot be named. What the tree beneath resolves that
// name to is contained by the tree beneath, and only it can be: a host disk
// follows a symlink inside the syscall, long after any name was derived.
//
// So a tree that hands out a subtree as a root of its own is asked for one, and
// the view is over that rather than over the whole area. A tree that hands out
// none is contained by the derivation alone, which is the whole containment a
// kind that resolves nothing but the name it was given needs.
//
// root is the area the view is over — the files mount, the key space — and must
// be a rooted, clean path, the mount table's rule for a prefix.
func (s Scope) View(fsys vfs.FileSystem, root string) (vfs.FileSystem, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("view root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	base, err := s.Path(root, "")
	if err != nil {
		return nil, fmt.Errorf("view: %w", err)
	}

	if sub := fsys.AsSubber(); sub != nil {
		if beneath := sub.Sub(base); beneath != nil {
			return view{fsys: beneath, scope: s}, nil
		}
	}
	return view{fsys: fsys, scope: s, base: base}, nil
}

// view is one area of one scope, seen from inside it. It stores nothing and
// resolves nothing: every verb puts the path under base and hands it on.
//
// base is where the scope stands in fsys, and is empty when fsys is already
// rooted there — the tree beneath handed out the subtree, so there is nothing
// left to prepend.
type view struct {
	fsys  vfs.FileSystem
	scope Scope
	base  string
}

// at is the path name stands for beneath the view. Joining under base is what
// contains name: a spelling that climbs lands on base rather than above it.
//
// base itself came from Scope.Path, which is the whole property — a second way
// to spell the layout here is a way to spell it differently.
func (v view) at(name string) string {
	return path.Join("/", v.base, path.Join("/", name))
}

// from maps a path the tree beneath spelled back into the view's vocabulary,
// which is what Readlink has to answer in. One outside the scope is refused
// rather than handed back: a path is observable even when what stands at it is
// not.
func (v view) from(name string) (string, error) {
	base := v.at("")
	clean := path.Clean(name)
	if clean == base {
		return "/", nil
	}
	prefix := base
	if prefix != "/" {
		prefix += "/"
	}
	if inside, found := strings.CutPrefix(clean, prefix); found {
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
	return v.fsys.Create(ctx, v.at(name))
}

func (v view) Mkdir(ctx context.Context, name string) error {
	return v.fsys.Mkdir(ctx, v.at(name))
}

// Remove refuses the root: a view that deleted the scope it stands for would
// answer nothing afterwards.
func (v view) Remove(ctx context.Context, name string) error {
	if isRoot(name) {
		return v.refuseRoot("remove")
	}
	return v.fsys.Remove(ctx, v.at(name))
}

// RemoveAll empties the root rather than deleting it, for the reason Remove
// refuses one.
func (v view) RemoveAll(ctx context.Context, name string) error {
	inside := v.at(name)
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
	return v.fsys.Read(ctx, v.at(name))
}

func (v view) Write(ctx context.Context, name string, data []byte) error {
	return v.fsys.Write(ctx, v.at(name), data)
}

// ReadDir names the children as the tree beneath named them: a name is the
// final element of a path, and deriving the path did not change that.
func (v view) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	return v.fsys.ReadDir(ctx, v.at(name))
}

// Stat names the root "/" rather than the workspace: what the scope is called
// is the layout speaking, and a caller inside a view is not told the layout.
func (v view) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	info, err := v.fsys.Stat(ctx, v.at(name))
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
	return v.fsys.Rename(ctx, v.at(oldPath), v.at(newPath))
}

func (v view) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	return v.fsys.Chmod(ctx, v.at(name), mode)
}

// Capabilities forwards, per path: what a view is depends entirely on what
// stands behind it at the path the scope derives.
func (v view) Capabilities(name string) vfs.Caps {
	return v.fsys.Capabilities(v.at(name))
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

// AsSubber returns the subtrees of the tree beneath, or nil when it hands out
// none. What comes back is a root of its own and is not wrapped again: the
// scope is already spent on the path that named it.
func (v view) AsSubber() vfs.Subber {
	if sub := v.fsys.AsSubber(); sub != nil {
		return viewSubber{view: v, sub: sub}
	}
	return nil
}

type viewSubber struct {
	view view
	sub  vfs.Subber
}

func (s viewSubber) Sub(name string) vfs.FileSystem { return s.sub.Sub(s.view.at(name)) }

type viewSymlinker struct {
	view view
	link vfs.Symlinker
}

// Symlink derives both paths. The target is a path like any other here, so a
// spelling that climbs lands on the scope root and one naming another scope
// lands inside this one.
func (s viewSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	return s.link.Symlink(ctx, s.view.at(oldPath), s.view.at(newPath))
}

// Readlink answers in the view's vocabulary. A link planted behind the view
// that stands for a path outside the scope has no name here and is refused.
func (s viewSymlinker) Readlink(ctx context.Context, name string) (string, error) {
	target, err := s.link.Readlink(ctx, s.view.at(name))
	if err != nil {
		return "", err
	}
	return s.view.from(target)
}

// Lstat derives the path and names the root "/", for the reason Stat does. What
// comes back describes an entry rather than naming a path, so there is nothing
// here to map back into the view's vocabulary.
func (s viewSymlinker) Lstat(ctx context.Context, name string) (vfs.FileInfo, error) {
	info, err := s.link.Lstat(ctx, s.view.at(name))
	if err == nil && isRoot(name) {
		info.Name = "/"
	}
	return info, err
}

type viewStreamer struct {
	view   view
	stream vfs.Streamer
}

func (s viewStreamer) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	return s.stream.OpenRead(ctx, s.view.at(name))
}

func (s viewStreamer) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	return s.stream.OpenWrite(ctx, s.view.at(name))
}

type viewTruncater struct {
	view view
	cut  vfs.Truncater
}

func (c viewTruncater) Truncate(ctx context.Context, name string, size int64) error {
	return c.cut.Truncate(ctx, c.view.at(name), size)
}
