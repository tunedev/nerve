package library

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Spec is one library an operator asked for: the kind of document it holds, and
// the root in the tree it holds them under.
//
// The two are separate because they answer different questions. The kind says
// what makes a directory one of these documents; the root says which mount the
// library sits on. Two kinds may share one root, and one kind may sit on a
// different mount in a different build, without either fact reaching the other.
type Spec struct {
	Kind Kind
	Root string
}

// Catalog is the libraries a process was configured with, one per kind, reached
// by the name the kind carries.
//
// It is what makes a document kind config rather than code. A kind nothing in
// this package names arrives as a Spec, gets a registry like the two that are
// named, and answers the same eight verbs; adding one is a line in a config
// document, and no file here changes.
type Catalog struct {
	libraries map[string]*Registry
	specs     []Spec
}

// NewCatalog builds the library each spec names over fsys, ranking every one of
// them with match.
//
// It stops at the first spec it cannot honour, so a process never boots holding
// half the libraries its config asked for. Two specs of one kind are refused
// with fs.ErrExist: a kind names one library, and two under one name make which
// one a caller gets depend on the order the config was written in.
//
// A catalog over no specs is a build with no libraries rather than an error, the
// way a mount table over no mounts is a tree with nothing in it.
func NewCatalog(fsys vfs.FileSystem, match Matcher, specs []Spec) (*Catalog, error) {
	catalog := &Catalog{libraries: make(map[string]*Registry, len(specs)), specs: slices.Clone(specs)}

	for _, spec := range specs {
		if _, taken := catalog.libraries[spec.Kind.Name]; taken {
			return nil, fmt.Errorf("library of %s documents at %q: kind is already served: %w", spec.Kind.Name, spec.Root, fs.ErrExist)
		}
		registry, err := NewRegistry(fsys, spec.Kind, spec.Root, match)
		if err != nil {
			return nil, err
		}
		catalog.libraries[spec.Kind.Name] = registry
	}
	slices.SortFunc(catalog.specs, func(a, b Spec) int { return strings.Compare(a.Kind.Name, b.Kind.Name) })
	return catalog, nil
}

// Open returns the library of documents of the named kind. A kind no config
// named fails with fs.ErrNotExist saying which kinds this process has, because
// a misspelled kind is worth making legible where a caller reads it.
func (c *Catalog) Open(kind string) (*Registry, error) {
	registry, served := c.libraries[kind]
	if !served {
		return nil, fmt.Errorf("no library of %q documents; this process has %s: %w", kind, c.known(), fs.ErrNotExist)
	}
	return registry, nil
}

// Specs is what the catalog holds, sorted by kind, as an operator reads it back.
func (c *Catalog) Specs() []Spec {
	return slices.Clone(c.specs)
}

// known lists the configured kinds for an error message.
func (c *Catalog) known() string {
	kinds := slices.Sorted(maps.Keys(c.libraries))
	if len(kinds) == 0 {
		return "no libraries"
	}
	return strings.Join(kinds, ", ")
}
