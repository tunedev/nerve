package library

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

// Registry is a workspace's library of documents of one kind, held on the tree
// under root and discovered through match.
//
// The kind is the only thing that differs between a registry of skills and a
// registry of blueprints. Every verb below is written against it, so a kind
// nobody has thought of yet gets the same eight and this file does not change.
type Registry struct {
	fsys  vfs.FileSystem
	kind  Kind
	root  string
	match Matcher
}

// NewRegistry returns the registry over fsys keeping documents of kind under
// root, ranking them with match.
//
// root must be a rooted, clean path such as "/skills" — the mount table's rule
// for a prefix, because a library is where a mount was put. A registry with no
// matcher is refused rather than allowed to become a Search that panics the
// first time an agent asks a question, which is the startup failure invalid
// config is entitled to. So is a kind that cannot name its own file.
func NewRegistry(fsys vfs.FileSystem, kind Kind, root string, match Matcher) (*Registry, error) {
	if err := kind.valid(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("%s root %q is not a rooted clean path: %w", kind.Name, root, fs.ErrInvalid)
	}
	if match == nil {
		return nil, fmt.Errorf("%s registry with no matcher: %w", kind.Name, fs.ErrInvalid)
	}
	return &Registry{fsys: fsys, kind: kind, root: root, match: match}, nil
}

// Create writes doc into s, replacing the file of a document already there.
//
// Replacing, because the verb set has no update and the port's own Write is
// what this is: the way to change what a document says is to say it again. Only
// its own file is written, so the files beside it survive a rewrite that does
// not mention them.
func (r *Registry) Create(ctx context.Context, s workspace.Scope, doc Document) error {
	dir, err := r.dir(s, doc.Name)
	if err != nil {
		return fmt.Errorf("create %s: %w", r.kind.Name, err)
	}
	if err := validDescription(doc.Description); err != nil {
		return fmt.Errorf("create %s %q: %w", r.kind.Name, doc.Name, err)
	}
	if err := r.fsys.Mkdir(ctx, dir); err != nil {
		return fmt.Errorf("create %s %q: %w", r.kind.Name, doc.Name, err)
	}
	if err := r.fsys.Write(ctx, path.Join(dir, r.kind.Filename), encode(doc)); err != nil {
		return fmt.Errorf("create %s %q: %w", r.kind.Name, doc.Name, err)
	}
	return nil
}

// Get returns the whole document named name in s.
func (r *Registry) Get(ctx context.Context, s workspace.Scope, name string) (Document, error) {
	dir, err := r.dir(s, name)
	if err != nil {
		return Document{}, fmt.Errorf("get %s: %w", r.kind.Name, err)
	}
	doc, err := r.read(ctx, dir, name)
	if err != nil {
		return Document{}, fmt.Errorf("get %s %q: %w", r.kind.Name, name, err)
	}
	return doc, nil
}

// List returns what every document in s says about itself, sorted by name.
//
// A directory holding no file of this kind is not one of these and is passed
// over: the tree under a mount is a tree, and something else standing in it —
// including a document of another kind — is not a broken one of these. A file
// that will not decode is, and fails the whole call: a short list is
// indistinguishable from a correct one, and the caller is about to choose from
// the ones it was not told about.
func (r *Registry) List(ctx context.Context, s workspace.Scope) ([]Card, error) {
	dir, err := r.base(s)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", r.kind.Name, err)
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
		doc, err := r.read(ctx, path.Join(dir, entry.Name), entry.Name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s %q: %w", r.kind.Name, entry.Name, err)
		}
		cards = append(cards, doc.Card)
	}
	slices.SortFunc(cards, func(a, b Card) int { return strings.Compare(a.Name, b.Name) })
	return cards, nil
}

// Delete removes name from s, and everything the document kept beside its file.
//
// A document that is not there is not an error, so a delete that failed halfway
// is completed by running it again. That is RemoveAll's promise and this verb is
// RemoveAll.
func (r *Registry) Delete(ctx context.Context, s workspace.Scope, name string) error {
	dir, err := r.dir(s, name)
	if err != nil {
		return fmt.Errorf("delete %s: %w", r.kind.Name, err)
	}
	if err := r.fsys.RemoveAll(ctx, dir); err != nil {
		return fmt.Errorf("delete %s %q: %w", r.kind.Name, name, err)
	}
	return nil
}

// Search returns the documents in s that the matcher finds relevant to task,
// best first, at most limit of them. A limit of zero or less asks for all of
// them.
//
// It ranks cards and not bodies. What a document is for is one line by design,
// and an agent choosing between documents is answering the question that line
// exists to answer; reading every body to choose one is the escalation Get is
// for.
func (r *Registry) Search(ctx context.Context, s workspace.Scope, task string, limit int) ([]Match, error) {
	cards, err := r.List(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", r.kind.Name, err)
	}

	matches, err := r.match.Rank(ctx, task, cards)
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", r.kind.Name, err)
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// Tree returns every file the document named name is made of, as paths relative
// to its directory, sorted. Its own file is among them: a tree is what is there.
func (r *Registry) Tree(ctx context.Context, s workspace.Scope, name string) ([]string, error) {
	dir, err := r.dir(s, name)
	if err != nil {
		return nil, fmt.Errorf("tree %s: %w", r.kind.Name, err)
	}
	if _, err := r.fsys.Stat(ctx, path.Join(dir, r.kind.Filename)); err != nil {
		return nil, fmt.Errorf("tree %s %q: %w", r.kind.Name, name, err)
	}

	var found []string
	if err := r.walk(ctx, dir, "", &found); err != nil {
		return nil, fmt.Errorf("tree %s %q: %w", r.kind.Name, name, err)
	}
	slices.Sort(found)
	return found, nil
}

// Bundle returns the document named name in s together with the files beside
// it, so that everything needed to use it arrives in one call rather than one
// call per file over a mount that may be remote.
func (r *Registry) Bundle(ctx context.Context, s workspace.Scope, name string) (Bundle, error) {
	doc, err := r.Get(ctx, s, name)
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
		if rel == r.kind.Filename {
			continue
		}
		data, err := r.fsys.Read(ctx, path.Join(dir, rel))
		if err != nil {
			return Bundle{}, fmt.Errorf("bundle %s %q: %w", r.kind.Name, name, err)
		}
		files = append(files, File{Path: rel, Data: data})
	}
	return Bundle{Document: doc, Files: files}, nil
}

// Copy duplicates the document named name in from as into in to, files and all.
// The two scopes may be the same one, which is how a document is forked under a
// second name, or different ones, which is how it leaves the workspace it was
// written in.
//
// A destination already occupied is refused with fs.ErrExist rather than
// merged. Writing one document's files over another's leaves a directory
// holding both, and neither of them is the document anybody asked for.
func (r *Registry) Copy(ctx context.Context, from workspace.Scope, name string, to workspace.Scope, into string) error {
	source, err := r.dir(from, name)
	if err != nil {
		return fmt.Errorf("copy %s: %w", r.kind.Name, err)
	}
	dest, err := r.dir(to, into)
	if err != nil {
		return fmt.Errorf("copy %s: %w", r.kind.Name, err)
	}
	if source == dest {
		return fmt.Errorf("copy %s %q onto itself: %w", r.kind.Name, name, fs.ErrInvalid)
	}

	names, err := r.Tree(ctx, from, name)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if _, err := r.fsys.Stat(ctx, dest); err == nil {
		return fmt.Errorf("copy %s %q to %q: %w", r.kind.Name, name, into, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("copy %s %q to %q: %w", r.kind.Name, name, into, err)
	}

	for _, rel := range names {
		data, err := r.fsys.Read(ctx, path.Join(source, rel))
		if err != nil {
			return fmt.Errorf("copy %s %q: %w", r.kind.Name, name, err)
		}
		target := path.Join(dest, rel)
		if err := r.fsys.Mkdir(ctx, path.Dir(target)); err != nil {
			return fmt.Errorf("copy %s %q: %w", r.kind.Name, into, err)
		}
		if err := r.fsys.Write(ctx, target, data); err != nil {
			return fmt.Errorf("copy %s %q: %w", r.kind.Name, into, err)
		}
	}
	return nil
}

// read returns the document whose directory is dir. A directory with no file of
// this kind in it reports fs.ErrNotExist, which is what "there is none of these
// here" is.
func (r *Registry) read(ctx context.Context, dir, name string) (Document, error) {
	data, err := r.fsys.Read(ctx, path.Join(dir, r.kind.Filename))
	if err != nil {
		return Document{}, err
	}
	card, body, err := decode(data)
	if err != nil {
		return Document{}, err
	}
	card.Name = name
	return Document{Card: card, Body: body}, nil
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

// base is where s keeps its documents of this kind.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so listing
// it would report workspaces as documents and creating in it would put a
// document where a workspace belongs.
func (r *Registry) base(s workspace.Scope) (string, error) {
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(r.root, "")
}

// dir is where the document named name stands in s.
func (r *Registry) dir(s workspace.Scope, name string) (string, error) {
	if err := vfs.ValidSegment("document name", name); err != nil {
		return "", err
	}
	base, err := r.base(s)
	if err != nil {
		return "", err
	}
	return path.Join(base, name), nil
}
