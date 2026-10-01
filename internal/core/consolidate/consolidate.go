// Package consolidate is what happens to a workspace's beliefs when several of
// them say one thing: they are merged into one narrative, and the facts it
// merged stop being believed without being removed.
//
// It is the stage that keeps a long-lived workspace from answering a turn with
// six sentences that are one claim. What it writes is a memory like any other,
// superseding the ones it merged, so the log still says what was believed and
// when — a narrative is a new belief rather than an edit to an old one.
//
// Judging that two sentences say one thing is a model call and goes through the
// gateway, so no stage here holds a provider client and which endpoint reads a
// workspace's beliefs is a line of config. It is the judgement dedup
// deliberately does not make: dedup catches a claim restated word for word, and
// everything past that — a paraphrase, a fact that narrows another — is here.
//
// A build with no consolidating endpoint has no consolidator, which it finds
// out where it wires the pipeline rather than on the first workspace it
// commits.
package consolidate

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Consolidator merges the related memories a workspace believes into
// narratives.
type Consolidator struct {
	memories *memory.Store
	merge    llm.FactConsolidator
}

// New returns the consolidator reading and writing memories in memories and
// merging through merge.
//
// A nil endpoint is refused. It is what the gateway answers when nothing in the
// chain merges facts, and a consolidator built on one would accept every
// workspace and fail on each one, which is a build that looks wired and merges
// nothing.
func New(memories *memory.Store, merge llm.FactConsolidator) (*Consolidator, error) {
	switch {
	case memories == nil:
		return nil, fmt.Errorf("consolidator needs a memory store: %w", fs.ErrInvalid)
	case merge == nil:
		return nil, fmt.Errorf("consolidator needs an endpoint; no configured endpoint merges facts: %w", fs.ErrInvalid)
	}
	return &Consolidator{memories: memories, merge: merge}, nil
}

// Consolidate merges the related memories s believes into narratives, each
// naming source as the conversation the merge was made during, and answers with
// what it wrote.
//
// The whole believed set goes to the endpoint in one call. Which facts are
// related is the judgement being asked for, and a caller that grouped them
// first would have made it already.
//
// A workspace believing fewer than two things has nothing to merge and no
// endpoint is called for it, which is what keeps consolidating on every commit
// from paying a model call per conversation.
//
// Nothing merged is not a failure. Most of what a workspace believes is
// unrelated to the rest of it, and a run that merges nothing is the ordinary
// answer for a workspace that has heard one more thing.
func (c *Consolidator) Consolidate(ctx context.Context, s workspace.Scope, source string) ([]memory.Memory, error) {
	believed, err := c.memories.Current(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("consolidate memories: %w", err)
	}
	if len(believed) < 2 {
		return nil, nil
	}

	narratives, err := c.merge.Consolidate(ctx, factsOf(believed))
	if err != nil {
		return nil, fmt.Errorf("consolidate memories: %w", err)
	}
	merges, err := mergesOf(believed, narratives)
	if err != nil {
		return nil, fmt.Errorf("consolidate memories: %w", err)
	}

	written := make([]memory.Memory, 0, len(merges))
	for _, m := range merges {
		one, err := c.memories.Supersede(ctx, s, source, m.replaced, []memory.Fact{m.fact})
		if err != nil {
			return nil, fmt.Errorf("consolidate memories: %w", err)
		}
		written = append(written, one...)
	}
	return written, nil
}

// merge is one narrative as the log will hold it: the memory to write, and the
// sequences it replaces.
type merge struct {
	fact     memory.Fact
	replaced []int
}

// mergesOf reads the endpoint's answer as writes against the memories it was
// shown, refusing an answer that does not address them.
//
// Two kinds of bad answer are told apart here, and they are told apart because
// only one of them is the endpoint's to make. A narrative naming a position
// that was not in the batch, naming one twice, merging a memory a second
// narrative already merged, or saying nothing at all, is an answer about
// something other than what was asked — nothing is written on the strength of
// it, because the rest of that answer cannot be trusted either.
//
// A narrative that names one fact, that merges two categories, or that merges a
// fact answering a question, is a judgement this stage declines rather than a
// broken answer: it is passed over and the rest of the run stands. A model is allowed to propose something the
// log does not do, and a workspace whose archival stopped because an endpoint
// kept proposing it would be worse off than one that merged a little less.
func mergesOf(believed []memory.Memory, narratives []llm.Narrative) ([]merge, error) {
	claimed := make(map[int]bool, len(believed))
	merges := make([]merge, 0, len(narratives))
	for _, narrative := range narratives {
		replaced := make([]int, 0, len(narrative.Facts))
		for _, at := range narrative.Facts {
			switch {
			case at < 0 || at >= len(believed):
				return nil, fmt.Errorf("narrative merges fact %d of %d it was shown: %w", at, len(believed), fs.ErrInvalid)
			case claimed[at]:
				return nil, fmt.Errorf("narrative merges memory %d, which another narrative already merged: %w", believed[at].Seq, fs.ErrInvalid)
			}
			claimed[at] = true
			replaced = append(replaced, believed[at].Seq)
		}
		if len(replaced) < 2 || !oneKind(believed, narrative.Facts) || mergesAnAnswer(believed, narrative.Facts) {
			continue
		}
		if narrative.Body == "" {
			return nil, fmt.Errorf("narrative merging %v says nothing: %w", replaced, fs.ErrInvalid)
		}
		merges = append(merges, merge{fact: merged(believed, narrative), replaced: replaced})
	}
	return merges, nil
}

// oneKind reports whether every fact the narrative merges was filed under one
// category.
//
// It is the line between what this stage merges and what it declines. The
// category is what a fact was filed under, and a workspace asked for its
// instructions must not answer with a preference nobody told it to keep.
func oneKind(believed []memory.Memory, at []int) bool {
	kind := believed[at[0]].Fact.Kind
	for _, i := range at[1:] {
		if believed[i].Fact.Kind != kind {
			return false
		}
	}
	return true
}

// mergesAnAnswer reports whether any fact the narrative merges answers a
// question.
//
// An answer stands on its own because a later answer replaces it. Folded into a
// narrative that names no subject, the old answer would go on being believed
// beside the new one.
func mergesAnAnswer(believed []memory.Memory, at []int) bool {
	for _, i := range at {
		if believed[i].Fact.Subject != "" {
			return true
		}
	}
	return false
}

// merged is the memory a narrative becomes: the endpoint's sentence, filed
// under the category it merged, ranked at the highest of what it merged.
//
// The rank is derived rather than answered. A narrative merging what a user
// said never to do is still what they said never to do, and an endpoint that
// ranked its own sentence could demote it into the half a caller with limited
// room drops first.
func merged(believed []memory.Memory, narrative llm.Narrative) memory.Fact {
	fact := memory.Fact{Kind: believed[narrative.Facts[0]].Fact.Kind, Body: narrative.Body}
	for _, at := range narrative.Facts {
		if priority := believed[at].Fact.Priority; priority > fact.Priority {
			fact.Priority = priority
		}
	}
	return fact
}

// factsOf hands the memories to the endpoint in the endpoint's vocabulary.
//
// The subject is carried so the endpoint can leave an answer alone. The
// sequence, the source and the interval are dropped because the port has no
// use for them: what a consolidator judges is what the claims say, and a port
// carrying a log's numbering would make the numbering part of the model
// contract. The positions in this slice are what the answer names instead.
func factsOf(believed []memory.Memory) []llm.Fact {
	facts := make([]llm.Fact, len(believed))
	for i, m := range believed {
		facts[i] = llm.Fact{Kind: m.Fact.Kind, Subject: m.Fact.Subject, Body: m.Fact.Body, Priority: priorityOf(m.Fact.Priority)}
	}
	return facts
}

// priorityOf maps the store's scale onto the endpoint's. Both are the same
// three rungs and they are separate types for the reason the two Fact types
// are: how a workspace stores its memories is not part of the contract with a
// model endpoint.
func priorityOf(p memory.Priority) llm.Priority {
	switch p {
	case memory.Low:
		return llm.Low
	case memory.Normal:
		return llm.Normal
	case memory.High:
		return llm.High
	}
	return 0
}
