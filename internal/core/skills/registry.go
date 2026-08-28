package skills

import (
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

// Registry is the skill library of a workspace, held on the tree under root and
// discovered through match.
type Registry struct {
	fsys  vfs.FileSystem
	root  string
	match Matcher
}

// NewRegistry returns the registry over fsys keeping skills under root, ranking
// them with match.
//
// root must be a rooted, clean path such as "/skills" — the mount table's rule
// for a prefix, because a skill library is where a mount was put. A registry
// with no matcher is refused rather than allowed to become a Search that panics
// the first time an agent asks a question, which is the startup failure invalid
// config is entitled to.
func NewRegistry(fsys vfs.FileSystem, root string, match Matcher) (*Registry, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("skills root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	if match == nil {
		return nil, fmt.Errorf("registry with no matcher: %w", fs.ErrInvalid)
	}
	return &Registry{fsys: fsys, root: root, match: match}, nil
}

// Create writes sk into s, replacing the document of a skill already there.
//
// Replacing, because the verb set has no update and the port's own Write is
// what this is: the way to change what a skill says is to say it again. Only
// the document is written, so the files beside it survive a rewrite that does
// not mention them.
func (r *Registry) Create(ctx context.Context, s workspace.Scope, sk Skill) error {
	dir, err := r.dir(s, sk.Name)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := validDescription(sk.Description); err != nil {
		return fmt.Errorf("create %q: %w", sk.Name, err)
	}
	if err := r.fsys.Mkdir(ctx, dir); err != nil {
		return fmt.Errorf("create %q: %w", sk.Name, err)
	}
	if err := r.fsys.Write(ctx, path.Join(dir, Document), encode(sk)); err != nil {
		return fmt.Errorf("create %q: %w", sk.Name, err)
	}
	return nil
}

// Get returns the whole skill named name in s.
func (r *Registry) Get(ctx context.Context, s workspace.Scope, name string) (Skill, error) {
	dir, err := r.dir(s, name)
	if err != nil {
		return Skill{}, fmt.Errorf("get: %w", err)
	}
	sk, err := r.read(ctx, dir, name)
	if err != nil {
		return Skill{}, fmt.Errorf("get %q: %w", name, err)
	}
	return sk, nil
}

// List returns what every skill in s says about itself, sorted by name.
//
// A directory holding no document is not a skill and is passed over: the tree
// under a mount is a tree, and something else standing in it is not a broken
// skill. A document that will not decode is, and fails the whole call — a short
// list of skills is indistinguishable from a correct one, and the caller is
// about to choose from the ones it was not told about.
func (r *Registry) List(ctx context.Context, s workspace.Scope) ([]Card, error) {
	dir, err := r.library(s)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	entries, err := r.fsys.ReadDir(ctx, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", dir, err)
	}

	cards := make([]Card, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sk, err := r.read(ctx, path.Join(dir, entry.Name), entry.Name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", entry.Name, err)
		}
		cards = append(cards, sk.Card)
	}
	slices.SortFunc(cards, func(a, b Card) int { return strings.Compare(a.Name, b.Name) })
	return cards, nil
}

// Delete removes name from s, and everything the skill kept beside its
// document.
//
// A skill that is not there is not an error, so a delete that failed halfway is
// completed by running it again. That is RemoveAll's promise and this verb is
// RemoveAll.
func (r *Registry) Delete(ctx context.Context, s workspace.Scope, name string) error {
	dir, err := r.dir(s, name)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := r.fsys.RemoveAll(ctx, dir); err != nil {
		return fmt.Errorf("delete %q: %w", name, err)
	}
	return nil
}

// Search returns the skills in s that the matcher finds relevant to task, best
// first, at most limit of them. A limit of zero or less asks for all of them.
//
// It ranks cards and not bodies. What a skill is for is one line by design, and
// an agent choosing between skills is answering the question that line exists to
// answer; reading every body to choose one is the escalation Get is for.
func (r *Registry) Search(ctx context.Context, s workspace.Scope, task string, limit int) ([]Match, error) {
	cards, err := r.List(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	matches, err := r.match.Rank(ctx, task, cards)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// Tree returns every file the skill named name is made of, as paths relative to
// its directory, sorted. The document is among them: a tree is what is there.
func (r *Registry) Tree(ctx context.Context, s workspace.Scope, name string) ([]string, error) {
	dir, err := r.dir(s, name)
	if err != nil {
		return nil, fmt.Errorf("tree: %w", err)
	}
	if _, err := r.fsys.Stat(ctx, path.Join(dir, Document)); err != nil {
		return nil, fmt.Errorf("tree %q: %w", name, err)
	}

	var found []string
	if err := r.walk(ctx, dir, "", &found); err != nil {
		return nil, fmt.Errorf("tree %q: %w", name, err)
	}
	slices.Sort(found)
	return found, nil
}

// Bundle returns the skill named name in s together with the files beside its
// document, so that everything needed to run it arrives in one call rather than
// one call per file over a mount that may be remote.
func (r *Registry) Bundle(ctx context.Context, s workspace.Scope, name string) (Bundle, error) {
	sk, err := r.Get(ctx, s, name)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: %w", err)
	}
	names, err := r.Tree(ctx, s, name)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: %w", err)
	}
	dir, err := r.dir(s, name)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: %w", err)
	}

	files := make([]File, 0, len(names)-1)
	for _, rel := range names {
		if rel == Document {
			continue
		}
		data, err := r.fsys.Read(ctx, path.Join(dir, rel))
		if err != nil {
			return Bundle{}, fmt.Errorf("bundle %q: %w", name, err)
		}
		files = append(files, File{Path: rel, Data: data})
	}
	return Bundle{Skill: sk, Files: files}, nil
}

// Copy duplicates the skill named name in from as into in to, files and all.
// The two scopes may be the same one, which is how a skill is forked under a
// second name, or different ones, which is how a skill leaves the workspace it
// was written in.
//
// A destination already occupied is refused with fs.ErrExist rather than
// merged. Writing one skill's files over another's leaves a directory holding
// both, and neither of them is the skill anybody asked for.
func (r *Registry) Copy(ctx context.Context, from workspace.Scope, name string, to workspace.Scope, into string) error {
	source, err := r.dir(from, name)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	dest, err := r.dir(to, into)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if source == dest {
		return fmt.Errorf("copy %q onto itself: %w", name, fs.ErrInvalid)
	}

	names, err := r.Tree(ctx, from, name)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if _, err := r.fsys.Stat(ctx, dest); err == nil {
		return fmt.Errorf("copy %q to %q: %w", name, into, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("copy %q to %q: %w", name, into, err)
	}

	for _, rel := range names {
		data, err := r.fsys.Read(ctx, path.Join(source, rel))
		if err != nil {
			return fmt.Errorf("copy %q: %w", name, err)
		}
		target := path.Join(dest, rel)
		if err := r.fsys.Mkdir(ctx, path.Dir(target)); err != nil {
			return fmt.Errorf("copy %q: %w", into, err)
		}
		if err := r.fsys.Write(ctx, target, data); err != nil {
			return fmt.Errorf("copy %q: %w", into, err)
		}
	}
	return nil
}

// read returns the skill whose directory is dir. A directory with no document
// in it reports fs.ErrNotExist, which is what "there is no skill here" is.
func (r *Registry) read(ctx context.Context, dir, name string) (Skill, error) {
	data, err := r.fsys.Read(ctx, path.Join(dir, Document))
	if err != nil {
		return Skill{}, err
	}
	card, body, err := decode(data)
	if err != nil {
		return Skill{}, err
	}
	card.Name = name
	return Skill{Card: card, Body: body}, nil
}

// walk collects the files beneath dir into found, with their paths relative to
// it.
func (r *Registry) walk(ctx context.Context, dir, prefix string, found *[]string) error {
	entries, err := r.fsys.ReadDir(ctx, path.Join(dir, prefix))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := path.Join(prefix, entry.Name)
		if !entry.IsDir() {
			*found = append(*found, rel)
			continue
		}
		if err := r.walk(ctx, dir, rel, found); err != nil {
			return err
		}
	}
	return nil
}

// library is where s keeps its skills.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so listing
// it would report workspaces as skills and creating in it would put a skill
// where a workspace belongs.
func (r *Registry) library(s workspace.Scope) (string, error) {
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(r.root, "")
}

// dir is where the skill named name stands in s.
func (r *Registry) dir(s workspace.Scope, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	library, err := r.library(s)
	if err != nil {
		return "", err
	}
	return path.Join(library, name), nil
}
