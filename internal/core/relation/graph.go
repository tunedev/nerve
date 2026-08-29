package relation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Graph is the curated edges of every workspace on the tree under root, reached
// by the refs the caller names inside its own scope.
type Graph struct {
	fsys vfs.FileSystem
	root string
}

// NewGraph returns the graph keeping curated edges under root on fsys.
//
// root must be a rooted, clean path such as "/relations" — the mount table's
// rule for a prefix, because a graph is where a mount was put.
func NewGraph(fsys vfs.FileSystem, root string) (*Graph, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("relations root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Graph{fsys: fsys, root: root}, nil
}

// Link asserts e in s. Asserting an edge that is already there changes nothing:
// an edge is a fact and not a count of how many times it was stated.
//
// The subject is made before the write, which is what a tree needs and what a
// keyspace has nothing to do about.
func (g *Graph) Link(ctx context.Context, s workspace.Scope, e Edge) error {
	at, err := g.at(s, e)
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	if err := g.fsys.Mkdir(ctx, path.Dir(at)); err != nil {
		return fmt.Errorf("link %s: %w", e, err)
	}
	if err := g.fsys.Write(ctx, at, nil); err != nil {
		return fmt.Errorf("link %s: %w", e, err)
	}
	return nil
}

// Unlink withdraws e from s. An edge that was never asserted is not an error,
// so an unlink that failed halfway is completed by running it again.
//
// It removes the one edge and nothing under it. A keyspace lets a key and a
// prefix share a name, so reaching beneath the edge would withdraw edges
// something else on the same mount asserted.
func (g *Graph) Unlink(ctx context.Context, s workspace.Scope, e Edge) error {
	at, err := g.at(s, e)
	if err != nil {
		return fmt.Errorf("unlink: %w", err)
	}
	if err := g.fsys.Remove(ctx, at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unlink %s: %w", e, err)
	}
	return nil
}

// List returns the edges asserted out of from in s, sorted by predicate and
// then by the resource on the other end. A subject nobody linked from has no
// edges, which is an answer rather than an error.
//
// It answers for one subject because that is the question a curated graph is
// asked: what this resource is related to. The other direction — what is
// related to this — is a second index and not this verb read backwards.
func (g *Graph) List(ctx context.Context, s workspace.Scope, from Ref) ([]Edge, error) {
	subject, err := g.subject(s, from)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}

	var edges []Edge
	predicates, err := g.dirsIn(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("list relations of %s: %w", from, err)
	}
	for _, predicate := range predicates {
		kinds, err := g.dirsIn(ctx, path.Join(subject, predicate))
		if err != nil {
			return nil, fmt.Errorf("list relations of %s: %w", from, err)
		}
		for _, kind := range kinds {
			names, err := g.filesIn(ctx, path.Join(subject, predicate, kind))
			if err != nil {
				return nil, fmt.Errorf("list relations of %s: %w", from, err)
			}
			for _, name := range names {
				edges = append(edges, Edge{From: from, Predicate: predicate, To: Ref{Kind: kind, Name: name}})
			}
		}
	}
	slices.SortFunc(edges, compare)
	return edges, nil
}

// at is where e stands in s: the whole edge spelled as a path, which is what
// makes two writers asserting different edges write different files.
func (g *Graph) at(s workspace.Scope, e Edge) (string, error) {
	if err := validSegment("predicate", e.Predicate); err != nil {
		return "", err
	}
	if err := e.To.valid(); err != nil {
		return "", err
	}
	subject, err := g.subject(s, e.From)
	if err != nil {
		return "", err
	}
	return path.Join(subject, e.Predicate, e.To.Kind, e.To.Name), nil
}

// subject is where the edges out of from stand in s.
//
// User scope is refused: a scope with no workspace derives the path holding
// every workspace that user has, so an edge written there would stand where a
// workspace belongs and be listed as one.
func (g *Graph) subject(s workspace.Scope, from Ref) (string, error) {
	if err := from.valid(); err != nil {
		return "", err
	}
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(g.root, path.Join(from.Kind, from.Name))
}

// dirsIn returns the names of the directories directly under dir; filesIn, the
// names of what is not one. A dir nothing is under holds nothing, on a tree
// where it was never made and on a keyspace where a prefix exists only because
// keys are beneath it.
//
// What is not of the shape its level expects is passed over rather than
// refused: the tree under a mount is a tree, and something else standing in it
// is not a broken edge.
func (g *Graph) dirsIn(ctx context.Context, dir string) ([]string, error) {
	return g.namesIn(ctx, dir, true)
}

func (g *Graph) filesIn(ctx context.Context, dir string) ([]string, error) {
	return g.namesIn(ctx, dir, false)
}

// namesIn is the child names of dir of one shape or the other.
func (g *Graph) namesIn(ctx context.Context, dir string, wantDirs bool) ([]string, error) {
	entries, err := g.fsys.ReadDir(ctx, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() == wantDirs {
			names = append(names, entry.Name)
		}
	}
	return names, nil
}

// compare orders two edges out of one subject: by predicate, then by the kind
// and name of the resource on the other end.
func compare(a, b Edge) int {
	return cmp.Or(
		strings.Compare(a.Predicate, b.Predicate),
		strings.Compare(a.To.Kind, b.To.Kind),
		strings.Compare(a.To.Name, b.To.Name),
	)
}
