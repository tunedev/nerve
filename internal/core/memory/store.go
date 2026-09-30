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

// Add writes the facts s does not already believe as the next memories, each
// naming source as the session it came from, and answers with what it wrote.
//
// A fact restating something the workspace already believes is not written
// again, so the answer can be shorter than the batch and empty for a
// conversation that said nothing new. That is the rule the log needs rather
// than one a caller keeps: a restated fact that became a second memory would be
// counted twice by everything reading the log, and every writer would have to
// remember not to write it.
//
// Restated means restated. Two facts are the same claim when they were filed
// under the same category and say the same thing, read as text rather than as
// bytes; a paraphrase is a different claim here and is merged, if it is merged,
// where meaning is judged.
//
// A fact naming a subject answers a question, and it replaces every believed
// memory answering that question under its kind: the memory it contradicts
// stops being believed at the moment the fact is written, and a question about
// an earlier moment still answers with it. Only the last answer a batch gives to
// a question is written.
//
// Adding nothing writes nothing and is not an error: a conversation with
// nothing worth remembering in it is the ordinary case, not a failed commit.
//
// A batch carrying a fact that cannot be stored is refused whole, including one
// a later answer in the batch would have left unwritten.
func (st *Store) Add(ctx context.Context, s workspace.Scope, source string, facts []Fact) ([]Memory, error) {
	if err := validFacts(facts); err != nil {
		return nil, fmt.Errorf("add memories from session %q: %w", source, err)
	}
	var entries []entry
	if len(facts) > 0 {
		believed, err := st.Current(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("add memories from session %q: %w", source, err)
		}
		entries = reconcile(believed, fresh(believed, lastAnswers(facts)))
	}
	written, err := st.write(ctx, s, source, entries)
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
	entries := make([]entry, len(facts))
	for i, fact := range facts {
		entries[i] = entry{fact: fact, replaced: ordered}
	}

	written, err := st.write(ctx, s, source, entries)
	if err != nil {
		return nil, fmt.Errorf("supersede memories from session %q: %w", source, err)
	}
	return written, nil
}

// write appends each entry to the log as a memory from source, recording what
// it supersedes.
//
// Every entry is checked before any is written, so a batch carrying one bad
// fact leaves the log as it found it. A half-written batch is a workspace
// holding some of what a conversation said and no record of which half.
//
// Each memory is written once, to a path of its own, and nothing already
// written is read back and rewritten.
func (st *Store) write(ctx context.Context, s workspace.Scope, source string, entries []entry) ([]Memory, error) {
	scoped, err := st.held(s)
	if err != nil {
		return nil, err
	}
	// A source is a session name and carries that rule: a source that resolves
	// away names a conversation nobody had.
	if err := vfs.ValidSegment("source session", source); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := validSupersedes(e.replaced); err != nil {
			return nil, err
		}
		if err := validFact(e.fact); err != nil {
			return nil, err
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}

	seqs, aside, err := logSeqs(ctx, scoped, logDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		for _, seq := range e.replaced {
			if !slices.Contains(seqs, seq) {
				return nil, fmt.Errorf("memory %d is not in the log: %w", seq, fs.ErrNotExist)
			}
		}
	}
	next := nextSeq(seqs, aside)
	if err := scoped.Mkdir(ctx, logDir); err != nil {
		return nil, err
	}

	written := make([]Memory, 0, len(entries))
	for i, e := range entries {
		m := held{Fact: e.fact, Source: source, At: st.now().UTC(), Supersedes: e.replaced}
		data, err := encode(m)
		if err != nil {
			return nil, err
		}
		seq := next + i
		if err := scoped.Write(ctx, path.Join(logDir, name(seq)), data); err != nil {
			return nil, fmt.Errorf("memory %d: %w", seq, err)
		}
		written = append(written, Memory{Seq: seq, Fact: e.fact, Source: source, At: m.At, Supersedes: e.replaced})
	}
	return written, nil
}

// List returns every memory in the log of s, in the order it was written,
// superseded ones included, each carrying the interval it was believed over. A
// workspace nobody has committed a conversation into has none, which is not an
// error.
//
// What has been archived is not in it. It is out of every answer the workspace
// gives, live and as-of alike, and Archived is the verb that reads it back.
//
// A memory that will not decode fails the whole call. A short list is
// indistinguishable from a correct one, and the caller is about to build a
// turn's context out of it.
func (st *Store) List(ctx context.Context, s workspace.Scope) ([]Memory, error) {
	live, _, err := st.load(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	return live, nil
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

// load returns the memories in the log of s and the memories in its archive,
// each ascending by sequence and each carrying the interval it was believed
// over.
//
// The two are read together and their intervals are closed together, because a
// supersession reaches across them: a memory still in the log can be the one
// that replaced an archived memory, and one read without the other would report
// a supersession naming a memory nobody wrote.
func (st *Store) load(ctx context.Context, s workspace.Scope) ([]Memory, []Memory, error) {
	scoped, err := st.held(s)
	if err != nil {
		return nil, nil, err
	}
	seqs, archived, err := logSeqs(ctx, scoped, logDir)
	if err != nil {
		return nil, nil, err
	}

	all := make([]Memory, 0, len(seqs)+len(archived))
	for _, seq := range seqs {
		m, err := readMemory(ctx, scoped, logDir, seq)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, m)
	}
	for _, seq := range archived {
		m, err := readMemory(ctx, scoped, path.Join(logDir, archiveName), seq)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, m)
	}
	slices.SortFunc(all, func(a, b Memory) int { return a.Seq - b.Seq })
	if err := closeIntervals(all); err != nil {
		return nil, nil, err
	}

	live, aside := make([]Memory, 0, len(seqs)), make([]Memory, 0, len(archived))
	for _, m := range all {
		if _, moved := slices.BinarySearch(archived, m.Seq); moved {
			aside = append(aside, m)
			continue
		}
		live = append(live, m)
	}
	return live, aside, nil
}

// readMemory returns the memory at seq in dir.
func readMemory(ctx context.Context, fsys vfs.FileSystem, dir string, seq int) (Memory, error) {
	data, err := fsys.Read(ctx, path.Join(dir, name(seq)))
	if err != nil {
		return Memory{}, fmt.Errorf("memory %d: %w", seq, err)
	}
	m, err := decode(data)
	if err != nil {
		return Memory{}, fmt.Errorf("memory %d: %w", seq, err)
	}
	return Memory{Seq: seq, Fact: m.Fact, Source: m.Source, At: m.At, Supersedes: m.Supersedes}, nil
}

// logSeqs returns the sequence of every memory in dir and of every memory in
// its archive, each ascending.
func logSeqs(ctx context.Context, fsys vfs.FileSystem, dir string) ([]int, []int, error) {
	seqs, err := seqsIn(ctx, fsys, dir)
	if err != nil {
		return nil, nil, err
	}
	archived, err := seqsIn(ctx, fsys, path.Join(dir, archiveName))
	if err != nil {
		return nil, nil, err
	}
	return seqs, archived, nil
}

// seqsIn returns the sequence of every memory directly in dir, ascending. A
// directory nothing has been written into yet has none, which is what an
// archive nobody has moved anything into looks like.
//
// The sequences come from the names on the mount rather than from a count
// something keeps, so a workspace whose caller was interrupted mid-batch still
// says what it holds. Anything in the directory that is not a memory — the
// archive, and whatever else a mount put there — is passed over.
func seqsIn(ctx context.Context, fsys vfs.FileSystem, dir string) ([]int, error) {
	children, err := fsys.ReadDir(ctx, dir)
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

// nextSeq is the sequence the next memory takes: one past the highest the
// workspace has ever handed out, archive included.
//
// A sequence handed out twice would put two facts under one name and make every
// supersession naming it ambiguous, so a workspace whose whole log has been
// archived counts on from where it left off rather than from one.
func nextSeq(seqs, archived []int) int {
	next := 1
	for _, taken := range [][]int{seqs, archived} {
		if len(taken) > 0 && taken[len(taken)-1] >= next {
			next = taken[len(taken)-1] + 1
		}
	}
	return next
}

// logDir is where a scope keeps its memories seen from inside it, which is the
// ground of the view and so the whole of it.
const logDir = "/"

// held is the store as a caller inside s sees it: the memories s keeps are
// names in it rather than a path derived down to them.
//
// The view is what makes the scope a boundary rather than a prefix: a name is
// resolved by the subtree the tree beneath hands out, so a link standing at any
// component of the scope path is refused instead of followed. A path derived
// down to the log and handed to the whole area is resolved by the whole area,
// which follows that link.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so a memory
// written there would stand where a workspace belongs.
func (st *Store) held(s workspace.Scope) (vfs.FileSystem, error) {
	if s.Workspace == "" {
		return nil, fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.View(st.fsys, st.root)
}
