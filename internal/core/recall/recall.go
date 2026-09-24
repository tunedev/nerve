// Package recall is the context route: what a turn is answered from. A
// workspace's profile — the memories it believes, most worth keeping first —
// and the documents hybrid retrieval finds for what the turn asks.
//
// Retrieval is read through the tree. The fusion is a mount that answers a
// question with a ranking of document keys, and the corpus is the mount those
// keys name. Neither is built here and neither is known by kind, so which
// rankers stand behind the fusion and where the corpus is kept are config.
//
// A ranking is over the whole index and can name any workspace's document. A
// key counts only when it lies inside the caller's scope, and its body is read
// through the scope's view of the corpus, so another workspace's document is
// never read on this route whatever the ranking named.
package recall

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Document is one retrieved document: its key inside the caller's scope and
// its body.
type Document struct {
	Key  string
	Body string
}

// Context is what one turn is answered from.
type Context struct {
	// Memories is the profile: what the workspace believes, highest priority
	// first and in the order written within a priority.
	Memories []memory.Memory
	// Documents is what retrieval found for the turn, best first.
	Documents []Document
}

// Routes build a turn's context from the memory log and the retrieval mounts
// in the tree.
type Routes struct {
	memories *memory.Store
	tree     vfs.FileSystem
	fusion   string
	corpus   string
}

// NewRoutes returns the context route over memories and tree, asking the
// fusion mounted at fusion and reading document bodies from corpus. Both must
// be rooted, clean paths — the mount table's rule for a prefix.
func NewRoutes(memories *memory.Store, tree vfs.FileSystem, fusion, corpus string) (*Routes, error) {
	switch {
	case memories == nil:
		return nil, fmt.Errorf("context route with no memory store: %w", fs.ErrInvalid)
	case tree == nil:
		return nil, fmt.Errorf("context route with no tree: %w", fs.ErrInvalid)
	}
	for _, root := range []string{fusion, corpus} {
		if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
			return nil, fmt.Errorf("context route root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
		}
	}
	return &Routes{memories: memories, tree: tree, fusion: fusion, corpus: corpus}, nil
}

// Context returns what s's turn is answered from: its profile, at most limit
// memories of it, and the documents retrieval found for turn. A limit of zero
// or less asks for the whole profile.
//
// A turn that asks nothing is refused with fs.ErrInvalid.
func (r *Routes) Context(ctx context.Context, s workspace.Scope, turn string, limit int) (Context, error) {
	question, err := questionOf(turn)
	if err != nil {
		return Context{}, err
	}
	memories, err := r.profile(ctx, s, limit)
	if err != nil {
		return Context{}, fmt.Errorf("context for a turn: %w", err)
	}
	documents, err := r.retrieve(ctx, s, question)
	if err != nil {
		return Context{}, fmt.Errorf("context for a turn: %w", err)
	}
	return Context{Memories: memories, Documents: documents}, nil
}

// profile returns what s believes, highest priority first, at most limit of it.
func (r *Routes) profile(ctx context.Context, s workspace.Scope, limit int) ([]memory.Memory, error) {
	believed, err := r.memories.Current(ctx, s)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(believed, func(a, b memory.Memory) int { return int(b.Fact.Priority - a.Fact.Priority) })
	if limit > 0 && len(believed) > limit {
		believed = believed[:limit]
	}
	return believed, nil
}

// retrieve asks the fusion question and reads the body of every key it named
// that lies inside s.
func (r *Routes) retrieve(ctx context.Context, s workspace.Scope, question string) ([]Document, error) {
	ranking, err := r.tree.Read(ctx, path.Join(r.fusion, "query", question))
	if err != nil {
		return nil, fmt.Errorf("ask %q: %w", question, err)
	}
	base, err := s.Path("/", "")
	if err != nil {
		return nil, err
	}
	corpus, err := s.View(r.tree, r.corpus)
	if err != nil {
		return nil, err
	}

	var documents []Document
	for _, key := range keysOf(ranking) {
		inside, ok := within(base, key)
		if !ok {
			continue
		}
		body, err := corpus.Read(ctx, inside)
		if err != nil {
			return nil, fmt.Errorf("document %q: %w", inside, err)
		}
		documents = append(documents, Document{Key: inside, Body: string(body)})
	}
	return documents, nil
}

// questionOf is turn as a question the fusion can be asked under its query
// directory. A separator would name a place rather than ask something, so the
// words are kept and the separators are not.
func questionOf(turn string) (string, error) {
	question := strings.Join(strings.Fields(strings.ReplaceAll(turn, "/", " ")), " ")
	if err := vfs.ValidSegment("turn", question); err != nil {
		return "", err
	}
	return question, nil
}

// keysOf reads the keys out of a ranking: a heading line, then one row per
// result whose first tab-separated field is the key.
func keysOf(ranking []byte) []string {
	rows := strings.Split(strings.TrimSuffix(string(ranking), "\n"), "\n")
	keys := make([]string, 0, len(rows))
	for _, row := range rows[1:] {
		key, _, _ := strings.Cut(row, "\t")
		keys = append(keys, key)
	}
	return keys
}

// within reports whether key lies under base and answers with what is left of
// it there. A key spelled in anything but its clean form is not one a corpus
// holds, and is not inside anything.
func within(base, key string) (string, bool) {
	rooted := "/" + key
	if path.Clean(rooted) != rooted {
		return "", false
	}
	return strings.CutPrefix(rooted, base+"/")
}
