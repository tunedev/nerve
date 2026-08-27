// Package synth answers the port from values the process already has rather
// than from anything it stored. A mount over it holds a fixed set of names
// whose content is computed at the moment it is read: a greeting, the time of
// the last heartbeat, the version this binary was built at.
//
// It is read-only at every path, and says so, because the names are the
// plugin's rather than a caller's: there is nothing to create and nothing to
// delete. A plugin that needs one name to accept a write embeds a tree and
// overrides Write and Capabilities for that name, which is what devfs does for
// its sink.
//
// The tree is flat. Every entry is a direct child of the root, because a
// synthetic mount that needed a hierarchy would be describing something it
// keeps rather than something it computes.
package synth

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// dirMode and entryMode are what the root and an entry report. The bits are
// read-only because the descriptor is: a mode a Chmod cannot change should not
// claim a write is possible.
const (
	dirMode   = fs.ModeDir | 0o555
	entryMode = 0o444
)

// Entry is one name a synthetic mount answers for.
type Entry struct {
	// Content returns what the entry holds now. It is called per read, because
	// what a synthetic entry holds is a value the process has at that moment
	// rather than bytes anybody stored.
	Content func(ctx context.Context) ([]byte, error)

	// Changed reports when that content last changed. A nil Changed means the
	// entry has held the same content since the mount was made.
	Changed func() time.Time
}

// Tree is a mount over a fixed set of synthetic entries, safe for concurrent
// use because it holds nothing that a verb writes to.
type Tree struct {
	vfs.NoAccessors

	entries map[string]Entry
	names   []string
	made    time.Time
}

// NewTree returns a mount over entries, made now. Names are the entries' keys,
// and the map is not read again after this: a tree answers for the names it was
// built with.
func NewTree(entries map[string]Entry) *Tree {
	names := make([]string, 0, len(entries))
	held := make(map[string]Entry, len(entries))
	for name, entry := range entries {
		names = append(names, name)
		held[name] = entry
	}
	slices.Sort(names)
	return &Tree{entries: held, names: names, made: time.Now()}
}

// Made is when the mount came up, which is what an entry that never changes
// reports as its ModTime.
func (t *Tree) Made() time.Time { return t.made }

// Capabilities reports a read-only mount at every path. The names are the
// plugin's own, so there is nothing for a caller to make or unmake.
func (t *Tree) Capabilities(string) vfs.Caps { return vfs.Caps{ReadOnly: true} }

// Read returns what the entry at path holds now.
func (t *Tree) Read(ctx context.Context, name string) ([]byte, error) {
	entry, err := t.lookup(name)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("read %q, which is the root of a synthetic mount: %w", name, fs.ErrInvalid)
	}
	return entry.Content(ctx)
}

// ReadDir lists the entries, sorted by name. Only the root has children.
func (t *Tree) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	entry, err := t.lookup(name)
	if err != nil {
		return nil, err
	}
	if entry != nil {
		return nil, fmt.Errorf("read_dir %q, which is not a directory: %w", name, fs.ErrInvalid)
	}

	listed := make([]vfs.FileInfo, 0, len(t.names))
	for _, child := range t.names {
		info, err := t.info(ctx, child)
		if err != nil {
			return nil, err
		}
		listed = append(listed, info)
	}
	return listed, nil
}

// Stat describes path. An entry's size is the length of what it holds now, so
// describing it costs what reading it costs.
func (t *Tree) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	entry, err := t.lookup(name)
	if err != nil {
		return vfs.FileInfo{}, err
	}
	if entry == nil {
		return vfs.FileInfo{Name: "/", Mode: dirMode, ModTime: t.made}, nil
	}
	return t.info(ctx, Name(name))
}

// Create refuses: the names are the plugin's.
func (t *Tree) Create(_ context.Context, name string) error { return t.refuse("create", name) }

// Mkdir refuses: a synthetic mount is flat and its shape is not a caller's.
func (t *Tree) Mkdir(_ context.Context, name string) error { return t.refuse("mkdir", name) }

// Remove refuses: an entry computed from the process cannot be unmade.
func (t *Tree) Remove(_ context.Context, name string) error { return t.refuse("remove", name) }

// RemoveAll refuses, for the reason Remove does.
func (t *Tree) RemoveAll(_ context.Context, name string) error { return t.refuse("remove_all", name) }

// Write refuses: there is nowhere to put content the plugin does not compute.
func (t *Tree) Write(_ context.Context, name string, _ []byte) error {
	return t.refuse("write", name)
}

// Rename refuses: a synthetic name means what the plugin says it means.
func (t *Tree) Rename(_ context.Context, oldPath, _ string) error {
	return t.refuse("rename", oldPath)
}

// Chmod refuses: the mode is the descriptor's, and the descriptor is read-only.
func (t *Tree) Chmod(_ context.Context, name string, _ fs.FileMode) error {
	return t.refuse("chmod", name)
}

// refuse is what every mutating verb answers, spelled as the descriptor
// promises: read-only means fs.ErrPermission, not a bare failure.
func (t *Tree) refuse(verb, name string) error {
	return fmt.Errorf("%s %q on a read-only synthetic mount: %w", verb, name, fs.ErrPermission)
}

// lookup resolves a path to the entry it names. A nil entry with a nil error is
// the root, which is the one directory a synthetic mount has.
func (t *Tree) lookup(name string) (*Entry, error) {
	elems := elements(name)
	switch len(elems) {
	case 0:
		return nil, nil
	case 1:
		if entry, known := t.entries[elems[0]]; known {
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("%q is not an entry of this mount: %w", name, fs.ErrNotExist)
}

// info describes one entry by the name it is held under.
func (t *Tree) info(ctx context.Context, name string) (vfs.FileInfo, error) {
	entry := t.entries[name]
	content, err := entry.Content(ctx)
	if err != nil {
		return vfs.FileInfo{}, err
	}

	changed := t.made
	if entry.Changed != nil {
		changed = entry.Changed()
	}
	return vfs.FileInfo{Name: name, Size: int64(len(content)), Mode: entryMode, ModTime: changed}, nil
}

// Name is the entry a path names, and empty for the root or for anything
// deeper than the one level a synthetic mount has. A plugin embedding a tree
// uses it to recognise its own entries, which is how devfs finds its sink
// without matching a path that only ends in the same word.
func Name(name string) string {
	elems := elements(name)
	if len(elems) != 1 {
		return ""
	}
	return elems[0]
}

// elements splits a path into the names below the root, resolving it lexically
// so a caller is not made to clean paths a mount table would have cleaned.
func elements(name string) []string {
	clean := path.Clean("/" + strings.TrimPrefix(name, "/"))
	if clean == "/" {
		return nil
	}
	return strings.Split(clean[1:], "/")
}

// Fixed is an entry whose content was settled when the mount came up. It is
// most of what a synthetic mount holds: a version string, a hostname, a
// greeting. A trailing newline is added, because these are read by people.
func Fixed(content string) Entry {
	line := []byte(content + "\n")
	return Entry{Content: func(context.Context) ([]byte, error) { return slices.Clone(line), nil }}
}
