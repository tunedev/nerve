package files

import (
	"context"
	"fmt"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Walk returns everything beneath name in s, directories included, each named
// by where it stands relative to name.
//
// The order is the tree's: every directory's children by name, and a
// directory's own subtree before whatever follows it. That is what a caller
// reading the result as a tree wants, and it is not the same as sorting the
// paths — "a/x" sorts after "a.txt" and stands before it.
func (t *Tree) Walk(ctx context.Context, s workspace.Scope, name string) ([]Entry, error) {
	inside, err := t.inside(s)
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", name, err)
	}
	found, err := collect(ctx, inside, name, func(string, vfs.FileInfo) bool { return true })
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", name, err)
	}
	return found, nil
}

// Find returns everything beneath name in s whose own final element matches
// pattern, at any depth. It is the question "where is the file called this",
// which is why the pattern is matched against the name and not the path.
func (t *Tree) Find(ctx context.Context, s workspace.Scope, name, pattern string) ([]Entry, error) {
	want, err := compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("find under %q: %w", name, err)
	}
	inside, err := t.inside(s)
	if err != nil {
		return nil, fmt.Errorf("find under %q: %w", name, err)
	}
	found, err := collect(ctx, inside, name, func(rel string, _ vfs.FileInfo) bool {
		return want.matches(path.Base(rel))
	})
	if err != nil {
		return nil, fmt.Errorf("find under %q: %w", name, err)
	}
	return found, nil
}

// Glob returns everything in s whose whole path matches pattern, where the path
// is relative to the workspace root and "*" does not cross a separator. It is
// the question "what is at this shape of path", so "docs/*.md" is the documents
// one level down and says nothing about the ones below them.
//
// The pattern carries its own directories, so unlike Find there is nowhere to
// start it: a glob is anchored at the workspace root by what it is.
func (t *Tree) Glob(ctx context.Context, s workspace.Scope, pattern string) ([]Entry, error) {
	want, err := compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	inside, err := t.inside(s)
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	found, err := collect(ctx, inside, "/", func(rel string, _ vfs.FileInfo) bool {
		return want.matches(rel)
	})
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}
	return found, nil
}

// collect walks beneath name and returns the entries keep accepts.
func collect(ctx context.Context, inside vfs.FileSystem, name string, keep func(string, vfs.FileInfo) bool) ([]Entry, error) {
	var found []Entry
	if err := descend(ctx, inside, name, "", keep, &found); err != nil {
		return nil, err
	}
	return found, nil
}

// descend reads one directory and recurses, naming what it finds relative to
// the directory the walk started from.
func descend(ctx context.Context, inside vfs.FileSystem, base, prefix string, keep func(string, vfs.FileInfo) bool, found *[]Entry) error {
	entries, err := inside.ReadDir(ctx, path.Join(base, prefix))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := path.Join(prefix, entry.Name)
		if keep(rel, entry) {
			*found = append(*found, Entry{Path: rel, Info: entry})
		}
		if !entry.IsDir() {
			continue
		}
		if err := descend(ctx, inside, base, rel, keep, found); err != nil {
			return err
		}
	}
	return nil
}
