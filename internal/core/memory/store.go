package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Store is the memories of every workspace on the tree under root.
//
// It keeps nothing itself. It reads and writes the one FileSystem the mount
// table presents, at paths derived from the scope that owns them, so whether a
// workspace's memories sit on a disk, in a keyspace or in an object store is a
// mount rather than a decision made here.
type Store struct {
	fsys vfs.FileSystem
	root string
	now  func() time.Time
}

// NewStore returns the store keeping memories under root on fsys, stamping each
// one with now.
//
// root must be a rooted, clean path such as "/memories" — the mount table's
// rule for a prefix, because a store is where a mount was put.
func NewStore(fsys vfs.FileSystem, root string, now func() time.Time) (*Store, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("memory root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Store{fsys: fsys, root: root, now: now}, nil
}

// Add writes facts to s as the next memories, each naming source as the session
// it came from, and answers with what it wrote.
//
// Adding nothing writes nothing and is not an error: a conversation with
// nothing worth remembering in it is the ordinary case, not a failed commit.
//
// Every fact is checked before any is written, so a batch carrying one bad fact
// leaves the log as it found it. A half-written batch is a workspace holding
// some of what a conversation said and no record of which half.
//
// Each memory is written once, to a path of its own, and nothing already
// written is read back and rewritten.
func (st *Store) Add(ctx context.Context, s workspace.Scope, source string, facts []Fact) ([]Memory, error) {
	dir, err := st.dir(s)
	if err != nil {
		return nil, fmt.Errorf("add memories: %w", err)
	}
	if err := validSource(source); err != nil {
		return nil, fmt.Errorf("add memories: %w", err)
	}
	for _, fact := range facts {
		if err := validFact(fact); err != nil {
			return nil, fmt.Errorf("add memories from session %q: %w", source, err)
		}
	}
	if len(facts) == 0 {
		return nil, nil
	}

	seqs, err := st.seqs(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("add memories from session %q: %w", source, err)
	}
	next := 1
	if len(seqs) > 0 {
		next = seqs[len(seqs)-1] + 1
	}
	if err := st.fsys.Mkdir(ctx, dir); err != nil {
		return nil, fmt.Errorf("add memories from session %q: %w", source, err)
	}

	written := make([]Memory, 0, len(facts))
	for i, fact := range facts {
		m := held{Fact: fact, Source: source, At: st.now().UTC()}
		data, err := encode(m)
		if err != nil {
			return nil, fmt.Errorf("add memories from session %q: %w", source, err)
		}
		seq := next + i
		if err := st.fsys.Write(ctx, path.Join(dir, name(seq)), data); err != nil {
			return nil, fmt.Errorf("add memory %d from session %q: %w", seq, source, err)
		}
		written = append(written, Memory{Seq: seq, Fact: fact, Source: source, At: m.At})
	}
	return written, nil
}

// List returns every memory in s, in the order it was written. A workspace
// nobody has committed a conversation into has none, which is not an error.
//
// A memory that will not decode fails the whole call. A short list is
// indistinguishable from a correct one, and the caller is about to build a
// turn's context out of it.
func (st *Store) List(ctx context.Context, s workspace.Scope) ([]Memory, error) {
	dir, err := st.dir(s)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	seqs, err := st.seqs(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("list memories %q: %w", dir, err)
	}

	memories := make([]Memory, 0, len(seqs))
	for _, seq := range seqs {
		data, err := st.fsys.Read(ctx, path.Join(dir, name(seq)))
		if err != nil {
			return nil, fmt.Errorf("list memories: memory %d: %w", seq, err)
		}
		m, err := decode(data)
		if err != nil {
			return nil, fmt.Errorf("list memories: memory %d: %w", seq, err)
		}
		memories = append(memories, Memory{Seq: seq, Fact: m.Fact, Source: m.Source, At: m.At})
	}
	return memories, nil
}

// seqs returns the sequence of every memory in dir, ascending. A directory
// nothing has been written into yet has none.
//
// The sequences come from the names on the mount rather than from a count
// something keeps, so a workspace whose caller was interrupted mid-batch still
// says what it holds.
func (st *Store) seqs(ctx context.Context, dir string) ([]int, error) {
	children, err := st.fsys.ReadDir(ctx, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seqs := make([]int, 0, len(children))
	for _, child := range children {
		if seq, ok := seqOf(child.Name); ok {
			seqs = append(seqs, seq)
		}
	}
	slices.Sort(seqs)
	return seqs, nil
}

// dir is where s keeps its memories.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so a memory
// written there would stand where a workspace belongs.
func (st *Store) dir(s workspace.Scope) (string, error) {
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(st.root, "")
}
