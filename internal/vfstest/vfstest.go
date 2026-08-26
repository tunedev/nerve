// Package vfstest is the conformance suite every storage plugin runs. It is one
// test set rather than one per plugin: what the port promises is asserted here,
// so a new storage kind means running the suite against it instead of writing a
// second one.
//
// A plugin hands itself over and the suite drives it:
//
//	func TestConformance(t *testing.T) {
//		vfstest.Run(t, vfstest.Plugin{
//			New: func(*testing.T) vfs.FileSystem { return memfs.New() },
//		})
//	}
//
// Descriptors are honoured rather than assumed. Every case declares what it
// needs a plugin to claim before it means anything: a read-only mount is asked
// to refuse mutation instead of being asked to store, an append-only one is
// asked to add rather than replace, and a kind whose names are prefixes is not
// asked to make a directory. Optional verb sets are reached through the
// accessors, and a plugin that answers nil for one is not held to it.
//
// Broadcast is the one descriptor with no case here. It separates readers that
// each receive every entry from readers that compete for each one, and the port
// has no second reader identity to tell the two apart.
package vfstest

import (
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Plugin is what the suite needs to exercise a storage kind.
type Plugin struct {
	// New returns a filesystem with nothing in it for one case to have to
	// itself. It takes the case's *testing.T so a kind backed by something
	// outside the process can register its cleanup.
	New func(t *testing.T) vfs.FileSystem

	// Root is the path the suite works under, "/" when it is empty. It is the
	// plugin's own root as the suite sees it: the ground it stands on, which
	// Remove and Rename are held to refuse. A mount whose capability varies by
	// path is run once per path, and each run holds the plugin to what it
	// claims there.
	Root string
}

// Run drives a plugin through every case that applies to what it claims.
func Run(t *testing.T, p Plugin) {
	t.Helper()

	root := p.Root
	if root == "" {
		root = "/"
	}

	claimed := p.New(t).Capabilities(root)
	for _, one := range cases() {
		if one.when != nil && !one.when(claimed) {
			continue
		}
		t.Run(one.name, func(t *testing.T) {
			one.run(t, world{fsys: p.New(t), root: root})
		})
	}
}

// testCase is one assertion about the port, and what a plugin has to claim
// before it means anything.
type testCase struct {
	name string
	// when reports whether the case applies to what the plugin claims at its
	// root. A nil when applies to every plugin.
	when func(vfs.Caps) bool
	run  func(t *testing.T, w world)
}

func cases() []testCase {
	var all []testCase
	for _, set := range [][]testCase{
		fileCases(),
		dirCases(),
		removeCases(),
		renameCases(),
		descriptorCases(),
		accessorCases(),
	} {
		all = append(all, set...)
	}
	return all
}

// world is the filesystem one case runs against and where in it the case works.
// Names inside a case are relative to the plugin's root.
type world struct {
	fsys vfs.FileSystem
	root string
}

// at resolves a name a case spelled relative to the plugin's root.
func (w world) at(name string) string { return path.Join(w.root, name) }

// writable and the three below it are what a case needs a plugin to claim
// before the case says anything true. A read-only mount stores nothing, an
// append-only one replaces nothing, a read-destructive one answers a second
// read differently, and a kind whose names are prefixes has no directories.
func writable(c vfs.Caps) bool   { return !c.ReadOnly }
func replacing(c vfs.Caps) bool  { return !c.AppendOnly }
func repeatable(c vfs.Caps) bool { return !c.ReadDestructive }
func tree(c vfs.Caps) bool       { return !c.ObjectStore }

// readOnly and the three below it select the cases that exist only because a
// descriptor is claimed, which is where the descriptor's meaning is asserted.
func readOnly(c vfs.Caps) bool        { return c.ReadOnly }
func appendOnly(c vfs.Caps) bool      { return c.AppendOnly }
func readDestructive(c vfs.Caps) bool { return c.ReadDestructive }
func objectStore(c vfs.Caps) bool     { return c.ObjectStore }

// competing is the reader side of broadcast: readers take entries from each
// other rather than each receiving every one.
func competing(c vfs.Caps) bool { return !c.Broadcast }

// needs combines the claims a case depends on.
func needs(claims ...func(vfs.Caps) bool) func(vfs.Caps) bool {
	return func(c vfs.Caps) bool {
		for _, claim := range claims {
			if !claim(c) {
				return false
			}
		}
		return true
	}
}

func mustWrite(t *testing.T, w world, name, content string) {
	t.Helper()
	if err := w.fsys.Write(t.Context(), w.at(name), []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", w.at(name), err)
	}
}

func mustMkdir(t *testing.T, w world, name string) {
	t.Helper()
	if err := w.fsys.Mkdir(t.Context(), w.at(name)); err != nil {
		t.Fatalf("Mkdir(%q) = %v", w.at(name), err)
	}
}

func mustRead(t *testing.T, w world, name string) string {
	t.Helper()
	content, err := w.fsys.Read(t.Context(), w.at(name))
	if err != nil {
		t.Fatalf("Read(%q) = %v", w.at(name), err)
	}
	return string(content)
}

// names is what ReadDir listed, which is what an ordering assertion compares.
func names(entries []vfs.FileInfo) []string {
	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	return listed
}
