package memory

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"slices"

	"github.com/tunedev/nerve/internal/core/workspace"
)

// The directory a workspace's archived memories stand in, inside the one its
// log stands in. The name is not a sequence, so the listing that finds memories
// passes over it.
const archiveName = "ARCHIVE"

// Archive moves the memories at seqs in s aside. What is archived is out of
// every answer the workspace gives — what it believes, and what it believed at
// a past moment — and is still there for the caller that asks for it.
//
// Nothing is deleted and nothing is rewritten. Each memory keeps the sequence
// it was written under, the log keeps counting past it, and Restore puts it
// back. That is the difference between a memory a workspace stopped keeping and
// one it never had.
//
// A sequence that is not in the log reports fs.ErrNotExist, and a batch naming
// one moves none of it: half an archival is a workspace that stopped believing
// some of what a caller asked it to set aside, with no record of which half.
//
// Archiving nothing is refused. It reads as a request that did something and
// would do nothing.
func (st *Store) Archive(ctx context.Context, s workspace.Scope, seqs []int) error {
	if err := st.move(ctx, s, seqs, toArchive); err != nil {
		return fmt.Errorf("archive memories %v: %w", seqs, err)
	}
	return nil
}

// Restore puts the memories at seqs in s back into the log, in the sequences
// they were written under. A sequence that is not in the archive reports
// fs.ErrNotExist, and a batch naming one moves none of it.
//
// What comes back is what was archived, interval and all. A memory something
// had already superseded is believed again no more than it was before it was
// set aside: restoring recovers the record, not the belief.
func (st *Store) Restore(ctx context.Context, s workspace.Scope, seqs []int) error {
	if err := st.move(ctx, s, seqs, fromArchive); err != nil {
		return fmt.Errorf("restore memories %v: %w", seqs, err)
	}
	return nil
}

// Archived returns the memories moved aside from s, in the order they were
// written, each carrying the interval it was believed over. A workspace nobody
// has archived from has none, which is not an error.
//
// This is what makes archiving a move rather than a deletion: what a caller
// set aside is out of the way of everything that reads the log, and still
// readable by the caller that names it.
func (st *Store) Archived(ctx context.Context, s workspace.Scope) ([]Memory, error) {
	_, archived, err := st.load(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("read archived memories: %w", err)
	}
	return archived, nil
}

// direction is which way a memory moves between the log and the archive.
type direction bool

const (
	toArchive   direction = true
	fromArchive direction = false
)

// move carries the memories at seqs between the log and the archive.
//
// Every sequence is checked before any memory moves, so a batch naming one that
// is not where it was said to be leaves the workspace as it found it.
func (st *Store) move(ctx context.Context, s workspace.Scope, seqs []int, way direction) error {
	dir, err := st.dir(s)
	if err != nil {
		return err
	}
	if err := validArchival(seqs); err != nil {
		return err
	}
	live, archived, err := st.logSeqs(ctx, dir)
	if err != nil {
		return err
	}

	from, to := dir, path.Join(dir, archiveName)
	holding := live
	if way == fromArchive {
		from, to, holding = to, dir, archived
	}
	for _, seq := range seqs {
		if !slices.Contains(holding, seq) {
			return fmt.Errorf("memory %d is not in %q: %w", seq, from, fs.ErrNotExist)
		}
	}
	if err := st.fsys.Mkdir(ctx, to); err != nil {
		return err
	}
	for _, seq := range seqs {
		if err := st.fsys.Rename(ctx, path.Join(from, name(seq)), path.Join(to, name(seq))); err != nil {
			return fmt.Errorf("memory %d: %w", seq, err)
		}
	}
	return nil
}

// validArchival returns fs.ErrInvalid for a batch no move can be made from: one
// naming nothing, one naming a sequence no memory can have, and one naming a
// memory twice, which would move it and then move it back.
func validArchival(seqs []int) error {
	if len(seqs) == 0 {
		return fmt.Errorf("no memory named: %w", fs.ErrInvalid)
	}
	seen := make(map[int]bool, len(seqs))
	for _, seq := range seqs {
		switch {
		case seq < 1:
			return fmt.Errorf("%d is not a sequence: %w", seq, fs.ErrInvalid)
		case seen[seq]:
			return fmt.Errorf("memory %d named twice: %w", seq, fs.ErrInvalid)
		}
		seen[seq] = true
	}
	return nil
}
