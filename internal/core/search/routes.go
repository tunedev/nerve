package search

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Routes are the four verbs a caller reaches a prefix through, over the tree
// the mount table presents and the ranker composition chose.
//
// The prefix is a parameter of every verb rather than a field, which is what
// makes one set of routes serve every area: memories, documents and skills are
// the same call with a different first argument.
type Routes struct {
	tree vfs.FileSystem
	rank Ranker
}

// NewRoutes returns the routes over tree, ranking with rank.
//
// Routes with no ranker are refused rather than allowed to become a Find that
// panics the first time an agent asks a question, which is the startup failure
// an incomplete composition is entitled to.
func NewRoutes(tree vfs.FileSystem, rank Ranker) (*Routes, error) {
	if tree == nil {
		return nil, fmt.Errorf("search routes with no tree: %w", fs.ErrInvalid)
	}
	if rank == nil {
		return nil, fmt.Errorf("search routes with no ranker: %w", fs.ErrInvalid)
	}
	return &Routes{tree: tree, rank: rank}, nil
}

// Glob returns the paths under prefix in s whose names match pattern, sorted.
//
// It is the cheapest route: names are what a listing already carries, so
// nothing under the prefix is read and a mount where reading consumes is
// matched as safely as a directory of files.
//
// The pattern is path.Match's, so a "*" stops at a separator and reaching into
// a subdirectory is spelled out. A pattern that is not one fails with
// fs.ErrInvalid rather than matching nothing, because a caller that mistyped a
// pattern and a prefix that holds nothing are different answers.
func (r *Routes) Glob(ctx context.Context, s workspace.Scope, prefix, pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("glob %q: %w: %w", pattern, err, fs.ErrInvalid)
	}
	area, err := r.area(s, prefix)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", prefix, err)
	}

	var found []string
	err = walk(ctx, area, "", func(name string, _ vfs.FileInfo) error {
		if ok, _ := path.Match(pattern, name); ok {
			found = append(found, name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("glob %q under %q: %w", pattern, prefix, err)
	}
	return found, nil
}

// Grep returns the lines under prefix in s that match expr, in path order and
// then in line order.
//
// It reads every entry, which is what makes it the answer to a question about
// content that no index was built for. A prefix where reading consumes is
// refused: a grep that emptied a queue would answer once and never again.
func (r *Routes) Grep(ctx context.Context, s workspace.Scope, prefix, expr string) ([]Line, error) {
	pattern, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("grep %q: %w: %w", expr, err, fs.ErrInvalid)
	}
	area, err := r.area(s, prefix)
	if err != nil {
		return nil, fmt.Errorf("grep %q: %w", prefix, err)
	}

	var found []Line
	err = walk(ctx, area, "", func(name string, _ vfs.FileInfo) error {
		data, err := read(ctx, area, name)
		if err != nil {
			return err
		}
		lines := bufio.NewScanner(strings.NewReader(string(data)))
		for number := 1; lines.Scan(); number++ {
			if pattern.MatchString(lines.Text()) {
				found = append(found, Line{Path: name, Number: number, Text: lines.Text()})
			}
		}
		return lines.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("grep %q under %q: %w", expr, prefix, err)
	}
	return found, nil
}

// Find returns the entries under prefix in s that the ranker finds relevant to
// query, best first, at most limit of them. A limit of zero or less asks for
// all of them.
//
// This is the route the rest exist beside: the caller says what it is looking
// for in its own words, and which prefix it is looking under. Everything that
// separates a memory from a skill is behind that prefix, and nothing here knows
// any of it.
func (r *Routes) Find(ctx context.Context, s workspace.Scope, prefix, query string, limit int) ([]Hit, error) {
	area, err := r.area(s, prefix)
	if err != nil {
		return nil, fmt.Errorf("find under %q: %w", prefix, err)
	}

	var entries []Entry
	err = walk(ctx, area, "", func(name string, _ vfs.FileInfo) error {
		data, err := read(ctx, area, name)
		if err != nil {
			return err
		}
		entries = append(entries, Entry{Path: name, Text: string(data)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find under %q: %w", prefix, err)
	}

	hits, err := r.rank.Rank(ctx, query, entries)
	if err != nil {
		return nil, fmt.Errorf("find under %q: %w", prefix, err)
	}
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Retrieve returns the content at name under prefix in s. It is the escalation
// from a hit to the thing it stands for: the routes above answer with paths so
// that a caller pays for the bodies it actually wants.
func (r *Routes) Retrieve(ctx context.Context, s workspace.Scope, prefix, name string) ([]byte, error) {
	area, err := r.area(s, prefix)
	if err != nil {
		return nil, fmt.Errorf("retrieve %q: %w", name, err)
	}
	data, err := read(ctx, area, name)
	if err != nil {
		return nil, fmt.Errorf("retrieve %q under %q: %w", name, prefix, err)
	}
	return data, nil
}

// area is the tree as a caller inside s sees it under prefix. Every route goes
// through it, so a path a caller names is derived by the scope and a path a
// route hands back is one the caller can name again.
func (r *Routes) area(s workspace.Scope, prefix string) (vfs.FileSystem, error) {
	return s.View(r.tree, prefix)
}

// read returns the content at name, refusing a path the mount says is consumed
// by reading it.
//
// Capabilities is not I/O, so the refusal costs nothing and happens before the
// damage rather than after it. It is fs.ErrInvalid and not a skip: a search
// that quietly left out the entries it could not read without destroying them
// is a short answer indistinguishable from a complete one.
func read(ctx context.Context, area vfs.FileSystem, name string) ([]byte, error) {
	if area.Capabilities(name).ReadDestructive {
		return nil, fmt.Errorf("read %q: reading it consumes it: %w", name, fs.ErrInvalid)
	}
	return area.Read(ctx, name)
}

// walk visits every file beneath at, with its path relative to the view root.
// Directories are descended and not visited: a search answers with the things
// that hold content.
//
// A prefix holding nothing is not an error and neither is a scope that has
// never written under it — an empty listing and a missing one are the same
// answer to "what is here".
func walk(ctx context.Context, area vfs.FileSystem, at string, visit func(string, vfs.FileInfo) error) error {
	entries, err := area.ReadDir(ctx, path.Join("/", at))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := path.Join(at, entry.Name)
		if entry.IsDir() {
			if err := walk(ctx, area, name, visit); err != nil {
				return err
			}
			continue
		}
		if err := visit(name, entry); err != nil {
			return err
		}
	}
	return nil
}
