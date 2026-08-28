package files_test

import (
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/files"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/files"

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}
)

// plugins are the two tree kinds a workspace's files sit on: one in memory with
// no links, one on a disk with them. Every verb is asked on both, because a
// passthrough that works over the store it was written against and not over the
// other one is passing nothing through.
var plugins = map[string]func(t *testing.T) vfs.FileSystem{
	"in memory": func(*testing.T) vfs.FileSystem { return memfs.New() },
	"on disk": func(t *testing.T) vfs.FileSystem {
		t.Helper()
		fsys, err := localfs.New(t.TempDir())
		if err != nil {
			t.Fatalf("localfs.New: %v", err)
		}
		return fsys
	},
}

func newTree(t *testing.T, plugin func(*testing.T) vfs.FileSystem) (*files.Tree, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin(t)); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	tree, err := files.NewTree(table, root)
	if err != nil {
		t.Fatalf("NewTree: %v", err)
	}
	return tree, table
}

// onBoth runs one case against every plugin, so a promise kept over one tree
// kind and broken over the other fails rather than passing on the half tried.
func onBoth(t *testing.T, run func(t *testing.T, tree *files.Tree, fsys vfs.FileSystem)) {
	t.Helper()
	for name, plugin := range plugins {
		t.Run(name, func(t *testing.T) {
			tree, fsys := newTree(t, plugin)
			run(t, tree, fsys)
		})
	}
}

// inside is the tree as a caller in s sees it. Tests reach for it to put
// content where the verbs under test will find it: this package navigates a
// workspace and never fills one, because carrying content in and out of a
// workspace is E6's resource routes.
func inside(t *testing.T, fsys vfs.FileSystem, s workspace.Scope) vfs.FileSystem {
	t.Helper()
	view, err := s.View(fsys, root)
	if err != nil {
		t.Fatalf("View(%+v): %v", s, err)
	}
	return view
}

func mustWrite(t *testing.T, fsys vfs.FileSystem, s workspace.Scope, name, content string) {
	t.Helper()
	view := inside(t, fsys, s)
	if err := view.Mkdir(t.Context(), path.Dir(name)); err != nil {
		t.Fatalf("Mkdir(%q): %v", path.Dir(name), err)
	}
	if err := view.Write(t.Context(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", name, err)
	}
}

func mustRead(t *testing.T, fsys vfs.FileSystem, s workspace.Scope, name string) string {
	t.Helper()
	data, err := inside(t, fsys, s).Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%q): %v", name, err)
	}
	return string(data)
}

// paths is what a walk found, as the caller reads it.
func paths(entries []files.Entry) []string {
	found := make([]string, 0, len(entries))
	for _, entry := range entries {
		found = append(found, entry.Path)
	}
	return found
}
