package files

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Tree is the file area of every workspace on the mount under root, walked from
// inside the scope that owns it.
type Tree struct {
	fsys vfs.FileSystem
	root string
}

// NewTree returns the tree over fsys holding workspace files under root.
//
// root must be a rooted, clean path such as "/files" — the mount table's rule
// for a prefix, because a file area is where a mount was put.
func NewTree(fsys vfs.FileSystem, root string) (*Tree, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("files root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Tree{fsys: fsys, root: root}, nil
}

// List returns the direct children of name in s, sorted by name.
func (t *Tree) List(ctx context.Context, s workspace.Scope, name string) ([]vfs.FileInfo, error) {
	inside, err := t.inside(s)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", name, err)
	}
	entries, err := inside.ReadDir(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", name, err)
	}
	return entries, nil
}

// Stat describes name in s without reading it.
func (t *Tree) Stat(ctx context.Context, s workspace.Scope, name string) (vfs.FileInfo, error) {
	inside, err := t.inside(s)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	info, err := inside.Stat(ctx, name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	return info, nil
}

// Mkdir makes name exist as a directory in s, creating the directories on the
// way to it.
func (t *Tree) Mkdir(ctx context.Context, s workspace.Scope, name string) error {
	inside, err := t.inside(s)
	if err != nil {
		return fmt.Errorf("mkdir %q: %w", name, err)
	}
	if err := inside.Mkdir(ctx, name); err != nil {
		return fmt.Errorf("mkdir %q: %w", name, err)
	}
	return nil
}

// Rename moves oldPath to newPath in s, replacing newPath if it is there. Both
// ends are inside the workspace, so this is a move and never an export.
func (t *Tree) Rename(ctx context.Context, s workspace.Scope, oldPath, newPath string) error {
	inside, err := t.inside(s)
	if err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	if err := inside.Rename(ctx, oldPath, newPath); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldPath, newPath, err)
	}
	return nil
}

// Delete removes name from s. A directory holding anything is refused, which is
// the port's promise and the one worth keeping as the default: the caller that
// meant the subtree says so with DeleteAll, and the caller that did not gets an
// error rather than a missing subtree.
func (t *Tree) Delete(ctx context.Context, s workspace.Scope, name string) error {
	inside, err := t.inside(s)
	if err != nil {
		return fmt.Errorf("delete %q: %w", name, err)
	}
	if err := inside.Remove(ctx, name); err != nil {
		return fmt.Errorf("delete %q: %w", name, err)
	}
	return nil
}

// DeleteAll removes name and everything beneath it from s. A path that is not
// there is not an error, so a delete that failed halfway is completed by
// running it again.
//
// The workspace root itself is emptied rather than removed: a workspace exists
// or does not by the lifecycle's word, and deleting every file in one is not
// the same act as deleting the workspace.
func (t *Tree) DeleteAll(ctx context.Context, s workspace.Scope, name string) error {
	inside, err := t.inside(s)
	if err != nil {
		return fmt.Errorf("delete all %q: %w", name, err)
	}
	if err := inside.RemoveAll(ctx, name); err != nil {
		return fmt.Errorf("delete all %q: %w", name, err)
	}
	return nil
}

// Symlink makes link stand for target in s, where target need not exist yet.
//
// Both paths are the view's to derive, which is the whole reason a view is what
// this package holds: a target is a path nothing beneath the core dispatches,
// so it is the one spelling a caller could otherwise use to name another
// workspace.
//
// A mount with no links is refused with fs.ErrInvalid rather than silently
// doing nothing, and the accessor is how that is discovered — a type assertion
// would interrogate the mount table and never the plugin.
func (t *Tree) Symlink(ctx context.Context, s workspace.Scope, target, link string) error {
	inside, err := t.inside(s)
	if err != nil {
		return fmt.Errorf("symlink %q to %q: %w", link, target, err)
	}
	links := inside.AsSymlinker()
	if links == nil {
		return fmt.Errorf("symlink %q to %q: the mount under %q has no links: %w", link, target, t.root, fs.ErrInvalid)
	}
	if err := links.Symlink(ctx, target, link); err != nil {
		return fmt.Errorf("symlink %q to %q: %w", link, target, err)
	}
	return nil
}

// inside is the tree as a caller in s sees it: a FileSystem rooted at the
// workspace, deriving every path it is handed.
//
// User scope is refused. A scope with no workspace derives the path holding
// every workspace that user has, so a view over it would list one workspace's
// files beside another's, and DeleteAll of "/" in it would empty all of them.
func (t *Tree) inside(s workspace.Scope) (vfs.FileSystem, error) {
	if s.Workspace == "" {
		return nil, fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.View(t.fsys, t.root)
}
