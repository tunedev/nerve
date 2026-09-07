// Package lifecycle is the four verbs a caller curates a workspace's memories
// through: set one aside, put it back, merge the ones that say one thing, and
// ask how a belief came to be what it is.
//
// It is where the log's own verbs and the merging stage meet one surface. What
// a caller reaches for after a conversation is committed is not the storage
// vocabulary — writing a memory, closing an interval — but the curation one,
// and the two are separated so that the store keeps no opinion about when a
// workspace should forget something and the merging stage keeps none about
// where a memory is kept.
//
// Nothing here deletes. Archiving moves a memory out of every answer the
// workspace gives and leaves it readable; restoring puts it back under the
// sequence it was written with; merging supersedes rather than edits. A
// workspace that curated itself into a corner can be read back out of it.
package lifecycle

import (
	"context"
	"fmt"
	"io/fs"
	"slices"

	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Consolidator merges the related memories a workspace believes into
// narratives.
//
// It is declared here rather than taken from the package that implements it
// because this is the consumer: what these routes need of that stage is one
// call, and a field of the concrete type would pull a model gateway into a
// package that reaches no endpoint.
type Consolidator interface {
	Consolidate(ctx context.Context, s workspace.Scope, source string) ([]memory.Memory, error)
}

// Routes are the lifecycle verbs over the memory log and the merging stage the
// composition chose.
type Routes struct {
	memories *memory.Store
	merge    Consolidator
}

// NewRoutes returns the routes over memories, merging through merge.
//
// Routes missing either half are refused here rather than allowed to become a
// verb that fails the first time an agent asks for it, which is the startup
// failure an incomplete composition is entitled to.
func NewRoutes(memories *memory.Store, merge Consolidator) (*Routes, error) {
	switch {
	case memories == nil:
		return nil, fmt.Errorf("memory lifecycle routes with no store: %w", fs.ErrInvalid)
	case merge == nil:
		return nil, fmt.Errorf("memory lifecycle routes with no consolidator: %w", fs.ErrInvalid)
	}
	return &Routes{memories: memories, merge: merge}, nil
}

// Archive moves the memories at seqs in s aside. What is archived is out of
// every answer the workspace gives and is still there for the caller that asks,
// which is what Restore is for.
func (r *Routes) Archive(ctx context.Context, s workspace.Scope, seqs []int) error {
	return r.memories.Archive(ctx, s, seqs)
}

// Restore puts the memories at seqs in s back into the log, under the sequences
// they were written with. What was superseded before it was set aside is
// superseded still: this recovers the record, not the belief.
func (r *Routes) Restore(ctx context.Context, s workspace.Scope, seqs []int) error {
	return r.memories.Restore(ctx, s, seqs)
}

// Consolidate merges the related memories s believes into narratives, each
// naming source as the conversation the merge was made during, and answers with
// what it wrote. A workspace with nothing to merge writes nothing, which is the
// ordinary answer rather than a failure.
func (r *Routes) Consolidate(ctx context.Context, s workspace.Scope, source string) ([]memory.Memory, error) {
	return r.merge.Consolidate(ctx, s, source)
}

// Lineage returns the memories seq's belief was made of and became: the memory
// named, everything it replaced, everything that replaced it, and so on in both
// directions, ascending by sequence and each carrying the interval it was
// believed over.
//
// This is the temporal question a listing cannot answer. A workspace says what
// it believes and what it believed at a moment; the lineage says how it got
// from one to the other, and each edge is dated by the interval the memory it
// points at ran over.
//
// The walk does not stop at the archive. A memory somebody set aside is still
// how the workspace came to believe what replaced it, and a lineage with a hole
// in it would report a belief that came from nowhere.
//
// A sequence no memory has reports fs.ErrNotExist. An empty lineage and one for
// a memory nobody wrote would otherwise read alike.
func (r *Routes) Lineage(ctx context.Context, s workspace.Scope, seq int) ([]memory.Memory, error) {
	if seq < 1 {
		return nil, fmt.Errorf("lineage of %d, which is not a sequence: %w", seq, fs.ErrInvalid)
	}
	all, err := r.log(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("lineage of memory %d: %w", seq, err)
	}
	at := make(map[int]memory.Memory, len(all))
	for _, m := range all {
		at[m.Seq] = m
	}
	if _, found := at[seq]; !found {
		return nil, fmt.Errorf("memory %d is not in the log: %w", seq, fs.ErrNotExist)
	}

	reached := map[int]bool{seq: true}
	for frontier := []int{seq}; len(frontier) > 0; {
		next := frontier[0]
		frontier = frontier[1:]
		for _, neighbour := range edgesOf(all, at[next]) {
			if !reached[neighbour] {
				reached[neighbour] = true
				frontier = append(frontier, neighbour)
			}
		}
	}

	lineage := make([]memory.Memory, 0, len(reached))
	for _, m := range all {
		if reached[m.Seq] {
			lineage = append(lineage, m)
		}
	}
	return lineage, nil
}

// edgesOf is what m is joined to in the supersession graph: the memories it
// replaced, and the memories that replaced it. The graph is walked undirected
// because the question is what a belief is made of, and the answer runs in both
// directions from wherever the caller entered it.
func edgesOf(all []memory.Memory, m memory.Memory) []int {
	joined := slices.Clone(m.Supersedes)
	for _, other := range all {
		if slices.Contains(other.Supersedes, m.Seq) {
			joined = append(joined, other.Seq)
		}
	}
	return joined
}

// log is every memory s holds, archived or not, ascending by sequence.
func (r *Routes) log(ctx context.Context, s workspace.Scope) ([]memory.Memory, error) {
	live, err := r.memories.List(ctx, s)
	if err != nil {
		return nil, err
	}
	archived, err := r.memories.Archived(ctx, s)
	if err != nil {
		return nil, err
	}
	all := append(live, archived...)
	slices.SortFunc(all, func(a, b memory.Memory) int { return a.Seq - b.Seq })
	return all, nil
}
