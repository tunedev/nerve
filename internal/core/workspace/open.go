package workspace

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirMode is what the root and an area report. The bits are read-only because
// the root is: a mode a Chmod cannot change should not claim a write is
// possible.
const dirMode = fs.ModeDir | 0o555

// Open returns the whole of s as one filesystem: every area the lifecycle
// covers standing as a top-level directory, and nothing beside them.
//
// This is what a process is handed, and it is where the scope stops being a
// derivation a caller could decline to use. A caller holding one view per area
// holds one filesystem per area and has to be told which is which; a caller
// holding this holds an ordinary directory tree whose root is the workspace.
// There is no name in it for the user segment or for the area root the layout
// put the workspace under, a path that climbs is cleaned before it is
// dispatched and lands on a name no area holds, and the root refuses to grow a
// name beside the areas.
//
// Each area is s.View over it, so the containment is the view's and none of it
// is repeated here. What is new is only that the areas are siblings.
//
// An area must be one segment, because here it is one name in one directory. A
// lifecycle may cover a nested area and delete a workspace from it; presenting
// one is refused rather than listed as a name that holds nothing.
func (l *Lifecycle) Open(s Scope) (vfs.FileSystem, error) {
	if s.Workspace == "" {
		return nil, fmt.Errorf("open: scope names no workspace: %w", fs.ErrInvalid)
	}

	table := vfs.NewMountTable()
	names := make([]string, 0, len(l.areas))
	for _, area := range l.areas {
		name := strings.TrimPrefix(area, "/")
		if strings.Contains(name, "/") {
			return nil, fmt.Errorf("open: area %q is more than one name: %w", area, fs.ErrInvalid)
		}
		view, err := s.View(l.fsys, area)
		if err != nil {
			return nil, fmt.Errorf("open %q: %w", area, err)
		}
		if err := table.Mount(area, view); err != nil {
			return nil, fmt.Errorf("open: %w", err)
		}
		names = append(names, name)
	}

	root := areaRoot{names: slices.Sorted(slices.Values(names)), made: time.Now()}
	if err := table.Mount("/", root); err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return table, nil
}

// areaRoot is the ground the areas stand on: a directory whose children are the
// areas and which holds nothing else. It stores nothing, because every path
// beneath an area's name belongs to that area's mount and never arrives here.
// What arrives is the root itself, and every name no area holds.
//
// It is read-only, and that is a boundary rather than a convenience. A process
// that could make a name beside the areas would be naming a place the scope
// derives nothing for, so the top of the tree stays the list of areas the
// lifecycle covers.
type areaRoot struct {
	names []string
	made  time.Time
}

func (r areaRoot) Create(_ context.Context, name string) error { return r.refuse("create", name) }
func (r areaRoot) Mkdir(_ context.Context, name string) error  { return r.refuse("mkdir", name) }
func (r areaRoot) Remove(_ context.Context, name string) error { return r.refuse("remove", name) }

func (r areaRoot) RemoveAll(_ context.Context, name string) error {
	return r.refuse("remove all of", name)
}

func (r areaRoot) Write(_ context.Context, name string, _ []byte) error {
	return r.refuse("write", name)
}

func (r areaRoot) Rename(_ context.Context, oldPath, _ string) error {
	return r.refuse("rename", oldPath)
}

func (r areaRoot) Chmod(_ context.Context, name string, _ fs.FileMode) error {
	return r.refuse("chmod", name)
}

// Read has nothing to answer with: the root is a directory, and every other
// name here is one no area holds.
func (r areaRoot) Read(_ context.Context, name string) ([]byte, error) {
	return nil, r.missing("read", name)
}

func (r areaRoot) Stat(_ context.Context, name string) (vfs.FileInfo, error) {
	if !isRoot(name) {
		return vfs.FileInfo{}, r.missing("stat", name)
	}
	return vfs.FileInfo{Name: "/", Mode: dirMode, ModTime: r.made}, nil
}

// ReadDir names the areas. It is the one listing a process inside a workspace
// gets that nothing stored: the areas are what the lifecycle covers rather than
// entries any plugin holds.
func (r areaRoot) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	if !isRoot(name) {
		return nil, r.missing("readdir", name)
	}
	entries := make([]vfs.FileInfo, len(r.names))
	for i, area := range r.names {
		entries[i] = vfs.FileInfo{Name: area, Mode: dirMode, ModTime: r.made}
	}
	return entries, nil
}

// Capabilities is the same at every path here, because the only paths that
// reach here are the root and names that are not there.
func (r areaRoot) Capabilities(string) vfs.Caps { return vfs.Caps{ReadOnly: true} }

// The root answers the optional verb sets rather than declining them, and that
// is what makes the read-only claim above true rather than nearly true. A
// workspace filesystem answers an accessor whenever any area does, so a root
// path reaching one is dispatched here regardless; declining would have it
// answered as a mount that has no such verb, which reads as a shape mismatch
// rather than as the refusal it is.
func (r areaRoot) AsSymlinker() vfs.Symlinker { return r }
func (r areaRoot) AsStreamer() vfs.Streamer   { return r }
func (r areaRoot) AsTruncater() vfs.Truncater { return r }

// AsSubber is the one accessor the root declines. A subtree here is an area,
// and an area is already a mount of its own in this table; handing one out
// would be a second way to reach it that no longer passes the scope.
func (r areaRoot) AsSubber() vfs.Subber { return nil }

func (r areaRoot) Symlink(_ context.Context, _, newPath string) error {
	return r.refuse("symlink", newPath)
}

func (r areaRoot) Readlink(_ context.Context, name string) (string, error) {
	return "", r.missing("readlink", name)
}

func (r areaRoot) OpenRead(_ context.Context, name string) (io.ReadCloser, error) {
	return nil, r.missing("open", name)
}

func (r areaRoot) OpenWrite(_ context.Context, name string) (io.WriteCloser, error) {
	return nil, r.refuse("open for writing", name)
}

func (r areaRoot) Truncate(_ context.Context, name string, _ int64) error {
	return r.refuse("truncate", name)
}

// refuse is what a mutating verb answers. It names why rather than only that:
// the areas are the whole of a workspace, so there is no place beside them to
// make one.
func (r areaRoot) refuse(verb, name string) error {
	return fmt.Errorf("%s %q: the areas %v are the whole of a workspace: %w", verb, name, r.names, fs.ErrPermission)
}

// missing is what a reading verb answers for a name that holds no content. The
// root is a directory; anything else here is a name no area holds.
func (r areaRoot) missing(verb, name string) error {
	if isRoot(name) {
		return fmt.Errorf("%s %q: it is a directory: %w", verb, name, fs.ErrInvalid)
	}
	return fmt.Errorf("%s %q: no area of the workspace holds it: %w", verb, name, fs.ErrNotExist)
}
