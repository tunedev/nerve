// Package commit is what happens to a conversation when it is over: the
// transcript is read, what is worth remembering is extracted from it into the
// workspace's memories, and the half nobody will read again is moved aside
// behind a summary.
//
// It is the stage that turns a record of what was said into claims that stand
// on their own, merges the claims that say one thing, and turns the rest of the
// conversation into a sentence. Nothing said is thrown away: no message is
// rewritten and none is removed — the cold ones move into the session's archive
// under the sequences they were said under, and the session retains what they
// said. Nothing believed is thrown away either: a merged fact is superseded
// rather than removed, so the log still says what was believed and when.
//
// Every stage is a model call and all of them go through the gateway, so no
// stage here holds a provider client and which endpoint reads a transcript is a
// line of config. A build with no extracting endpoint has no committer, one
// with no consolidating endpoint has no consolidator, and one with no
// summarising endpoint has no archiver — each of them found out where the
// pipeline is wired rather than on the first conversation.
//
// The order of the stages is the whole recovery story. Extraction reads the
// whole conversation and archival narrows it, so archival runs last: a commit
// interrupted before it has written its memories and merged them and not
// reduced anything, which running it again completes. The other order reduces
// the transcript first, and the second run extracts from a conversation whose
// cold half it can no longer see — the facts in it are lost to that run and to
// every later one. A repeat run restates facts and the log keeps one of each; a
// fact never extracted is not recoverable at all.
//
// Consolidation sits between them because it merges what the workspace
// believes, which is not complete until this conversation's facts are in it. A
// commit interrupted between writing and merging leaves the facts standing
// separately, which is a workspace that answers with more sentences than it
// needs rather than one missing a claim, and the next run merges them.
package commit

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/archive"
	"github.com/tunedev/nerve/internal/core/consolidate"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Result is what one commit did: the memories it wrote, the narratives it
// merged them into, and the session as it stands afterwards.
//
// The session is the session's state and not this run's. A commit that found
// the cold half already moved aside answers with the summary that was already
// there, which is the true statement about the conversation — and a caller
// comparing Archived against what it saw last is asking whether the
// conversation has grown, not whether an endpoint was called.
type Result struct {
	Memories []memory.Memory
	// Narratives are the memories consolidation wrote, each merging several of
	// the workspace's beliefs and superseding them. They are separate from
	// Memories because they are not what this conversation said: a caller
	// showing the user what a commit learned wants the first, and one asking
	// what changed about the workspace wants both.
	Narratives []memory.Memory
	Session    session.Session
}

// Committer reads a session, writes what is worth remembering from it, merges
// what the workspace now believes twice over, and moves aside what will not be
// read again.
type Committer struct {
	sessions     *session.Store
	memories     *memory.Store
	extract      llm.FactExtractor
	consolidator *consolidate.Consolidator
	archiver     *archive.Archiver
	keep         int
}

// New returns the committer reading sessions from sessions, writing memories to
// memories, extracting through extract, merging through consolidator, and
// reducing through archiver while keeping the last keep turns of a conversation
// as they were said. The archiver reads and writes the same sessions, and the
// consolidator the same memories.
//
// A nil extractor, consolidator or archiver is refused. Nil is what the gateway
// answers when nothing in the chain serves a capability, and a committer built
// on one would accept every session and fail on each one, which is a build that
// looks wired and remembers nothing.
//
// Keeping no turns hot is refused rather than read as "reduce everything": it
// is the setting that hands a caller a summary where the turn it is answering
// should be.
func New(sessions *session.Store, memories *memory.Store, extract llm.FactExtractor, consolidator *consolidate.Consolidator, archiver *archive.Archiver, keep int) (*Committer, error) {
	switch {
	case sessions == nil || memories == nil:
		return nil, fmt.Errorf("committer needs a session store and a memory store: %w", fs.ErrInvalid)
	case extract == nil:
		return nil, fmt.Errorf("committer needs an extractor; no configured endpoint extracts: %w", fs.ErrInvalid)
	case consolidator == nil:
		return nil, fmt.Errorf("committer needs a consolidator; no configured endpoint merges facts: %w", fs.ErrInvalid)
	case archiver == nil:
		return nil, fmt.Errorf("committer needs an archiver; no configured endpoint summarises: %w", fs.ErrInvalid)
	case keep < 1:
		return nil, fmt.Errorf("committer keeps %d turns hot, leaving a session with no live half: %w", keep, fs.ErrInvalid)
	}
	return &Committer{sessions: sessions, memories: memories, extract: extract, consolidator: consolidator, archiver: archiver, keep: keep}, nil
}

// Commit reads the session named name in s, extracts what is worth remembering
// from it into the workspace's memories, merges the beliefs that say one thing
// into narratives, and moves everything but the last few turns into the
// session's archive behind a summary. It answers with all three.
//
// A session nobody created reports fs.ErrNotExist, from the read: a commit of a
// misspelled name that quietly wrote nothing looks exactly like a conversation
// with nothing in it.
//
// A conversation with nothing worth remembering commits nothing and is not a
// failure, and neither does one that only said again what the workspace already
// believes: a fact restating a memory is not written a second time, so
// committing the same session twice leaves the log as the first commit left it.
// Reduction is the same shape: a second commit over a conversation that has not
// grown finds the hot half already within the policy and calls no endpoint. So
// is consolidation: the second run is shown the narratives the first one wrote,
// and a narrative merges with nothing.
func (c *Committer) Commit(ctx context.Context, s workspace.Scope, name string) (Result, error) {
	transcript, err := c.sessions.Get(ctx, s, name)
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}

	facts, err := c.extract.Extract(ctx, turnsOf(transcript.Messages))
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}
	written, err := c.memories.Add(ctx, s, name, factsOf(facts))
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}
	merged, err := c.consolidator.Consolidate(ctx, s, name)
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}

	reduced, err := c.archiver.Archive(ctx, s, name, c.keep)
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}
	return Result{Memories: written, Narratives: merged, Session: reduced}, nil
}

// turnsOf hands a transcript to the extractor in the extractor's vocabulary.
//
// The sequence and the stamp are dropped because the port has no use for them:
// what an extractor needs is who said what, in order, and a port carrying a
// store's numbering would make the numbering part of the model contract.
func turnsOf(messages []session.Message) []llm.Turn {
	turns := make([]llm.Turn, len(messages))
	for i, message := range messages {
		turns[i] = llm.Turn{Role: message.Role, Body: message.Body}
	}
	return turns
}

// factsOf maps what the endpoint answered onto what the store holds.
//
// The two Fact types are deliberately separate and this is the seam between
// them. llm speaks only its own vocabulary, the way Summariser takes a string
// and a count, so how a workspace stores its memories is not part of the
// contract with a model endpoint — and a hosted provider added later cannot
// change the shape of what is already on disk.
//
// A priority the endpoint answered off the scale is left off this scale too,
// and the store refuses the batch. An endpoint inventing a rank is not a fact
// to file under the nearest one.
func factsOf(extracted []llm.Fact) []memory.Fact {
	facts := make([]memory.Fact, len(extracted))
	for i, fact := range extracted {
		facts[i] = memory.Fact{Kind: fact.Kind, Body: fact.Body, Priority: priorityOf(fact.Priority)}
	}
	return facts
}

// priorityOf maps the extractor's scale onto the store's.
func priorityOf(p llm.Priority) memory.Priority {
	switch p {
	case llm.Low:
		return memory.Low
	case llm.Normal:
		return memory.Normal
	case llm.High:
		return memory.High
	}
	return 0
}
