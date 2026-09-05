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
// What is added is believed from the moment it is written until something
// supersedes it, which is what Supersede is for.
func (st *Store) Add(ctx context.Context, s workspace.Scope, source string, facts []Fact) ([]Memory, error) {
	written, err := st.write(ctx, s, source, nil, facts)
	if err != nil {
		return nil, fmt.Errorf("add memories from session %q: %w", source, err)
	}
	return written, nil
}

// Supersede writes facts to s as the memories replacing the ones at replaced,
// and answers with what it wrote. Each memory named in replaced stops being
// believed at the moment the first of these is written; none of them is edited
// or removed, so a question about a past moment still answers with them.
//
// A memory named in replaced that is not in the log reports fs.ErrNotExist.
// Closing an interval against a memory nobody wrote is a claim about nothing,
// and it is how a caller finds out its sequence came from somewhere else.
//
// Superseding with no facts is refused. It reads as a retraction and is not
// one: nothing would be written, so nothing would record that anything stopped
// being believed, and the call would silently do nothing.
func (st *Store) Supersede(ctx context.Context, s workspace.Scope, source string, replaced []int, facts []Fact) ([]Memory, error) {
	if len(facts) == 0 {
		return nil, fmt.Errorf("supersede memories from session %q: nothing to replace them with: %w", source, fs.ErrInvalid)
	}
	if len(replaced) == 0 {
		return nil, fmt.Errorf("supersede memories from session %q: no memory named: %w", source, fs.ErrInvalid)
	}
	ordered := slices.Clone(replaced)
	slices.Sort(ordered)

	written, err := st.write(ctx, s, source, ordered, facts)
	if err != nil {
		return nil, fmt.Errorf("supersede memories from session %q: %w", source, err)
	}
	return written, nil
}

// write appends facts to the log as memories from source, each recording
// replaced as what it supersedes.
//
// Every fact is checked before any is written, so a batch carrying one bad fact
// leaves the log as it found it. A half-written batch is a workspace holding
// some of what a conversation said and no record of which half.
//
// Each memory is written once, to a path of its own, and nothing already
// written is read back and rewritten.
func (st *Store) write(ctx context.Context, s workspace.Scope, source string, replaced []int, facts []Fact) ([]Memory, error) {
	dir, err := st.dir(s)
	if err != nil {
		return nil, err
	}
	if err := validSource(source); err != nil {
		return nil, err
	}
	if err := validSupersedes(replaced); err != nil {
		return nil, err
	}
	for _, fact := range facts {
		if err := validFact(fact); err != nil {
			return nil, err
		}
	}
	if len(facts) == 0 {
		return nil, nil
	}

	seqs, err := st.seqs(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, seq := range replaced {
		if !slices.Contains(seqs, seq) {
			return nil, fmt.Errorf("memory %d is not in the log: %w", seq, fs.ErrNotExist)
		}
	}
	next := 1
	if len(seqs) > 0 {
		next = seqs[len(seqs)-1] + 1
	}
	if err := st.fsys.Mkdir(ctx, dir); err != nil {
		return nil, err
	}

	written := make([]Memory, 0, len(facts))
	for i, fact := range facts {
		m := held{Fact: fact, Source: source, At: st.now().UTC(), Supersedes: replaced}
		data, err := encode(m)
		if err != nil {
			return nil, err
		}
		seq := next + i
		if err := st.fsys.Write(ctx, path.Join(dir, name(seq)), data); err != nil {
			return nil, fmt.Errorf("memory %d: %w", seq, err)
		}
		written = append(written, Memory{Seq: seq, Fact: fact, Source: source, At: m.At, Supersedes: replaced})
	}
	return written, nil
}

// List returns every memory in s, in the order it was written, superseded ones
// included, each carrying the interval it was believed over. A workspace nobody
// has committed a conversation into has none, which is not an error.
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
		memories = append(memories, Memory{Seq: seq, Fact: m.Fact, Source: m.Source, At: m.At, Supersedes: m.Supersedes})
	}
	if err := closeIntervals(memories); err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	return memories, nil
}

// AsOf returns the memories s believed at t, in the order they were written:
// every memory written by then that nothing had yet replaced.
//
// This is the whole of what the workspace held at that moment, not a diff
// against what it holds now, so a caller can rebuild the context a turn was
// answered from.
func (st *Store) AsOf(ctx context.Context, s workspace.Scope, t time.Time) ([]Memory, error) {
	memories, err := st.List(ctx, s)
	if err != nil {
		return nil, err
	}
	believed := make([]Memory, 0, len(memories))
	for _, m := range memories {
		if m.Believed(t) {
			believed = append(believed, m)
		}
	}
	return believed, nil
}

// Current returns the memories s believes, in the order they were written:
// every memory nothing has superseded.
//
// It reads no clock. A memory is current because nothing has replaced it, and
// asking as of the caller's now would answer differently on a caller whose
// clock is behind the store's.
func (st *Store) Current(ctx context.Context, s workspace.Scope) ([]Memory, error) {
	memories, err := st.List(ctx, s)
	if err != nil {
		return nil, err
	}
	current := make([]Memory, 0, len(memories))
	for _, m := range memories {
		if m.Until.IsZero() {
			current = append(current, m)
		}
	}
	return current, nil
}

// closeIntervals fills in when each memory stopped being believed, from the
// supersessions the later memories carry. Memories arrive ascending by
// sequence, so the first memory found naming one is the first that replaced it,
// and a second supersession of the same memory does not move the end.
//
// A memory naming a sequence that is not in the log, or one at or above its
// own, is refused with fs.ErrInvalid. Either would make an interval that ends
// before it began or one that never ends, and the caller would read a replaced
// fact as one the workspace still believes.
func closeIntervals(memories []Memory) error {
	at := make(map[int]int, len(memories))
	for i, m := range memories {
		at[m.Seq] = i
	}
	for _, m := range memories {
		for _, seq := range m.Supersedes {
			i, found := at[seq]
			if !found {
				return fmt.Errorf("memory %d supersedes %d, which is not in the log: %w", m.Seq, seq, fs.ErrInvalid)
			}
			if seq >= m.Seq {
				return fmt.Errorf("memory %d supersedes %d, which it does not follow: %w", m.Seq, seq, fs.ErrInvalid)
			}
			if memories[i].Until.IsZero() {
				memories[i].Until = m.At
			}
		}
	}
	return nil
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
