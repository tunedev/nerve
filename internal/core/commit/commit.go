// Package commit is what happens to a conversation when it is over: the
// transcript is read, what is worth remembering is extracted from it into the
// workspace's memories, and the half nobody will read again is moved aside
// behind a summary.
//
// It is the stage that turns a record of what was said into claims that stand
// on their own, and the rest of it into a sentence. Nothing said is thrown
// away: no message is rewritten and none is removed — the cold ones move into
// the session's archive under the sequences they were said under, and the
// session retains what they said.
//
// Both stages are model calls and both go through the gateway, so no stage here
// holds a provider client and which endpoint reads a transcript is a line of
// config. A build with no extracting endpoint has no committer, and a build
// with no summarising endpoint has no archiver and so no committer either.
// Both are found out where the pipeline is wired rather than on the first
// conversation.
//
// The order of the two stages is the whole recovery story. Extraction reads the
// whole conversation and archival narrows it, so archival runs last: a commit
// interrupted between them has written its memories and not reduced anything,
// which running it again completes. The other order reduces the transcript
// first, and the second run extracts from a conversation whose cold half it can
// no longer see — the facts in it are lost to that run and to every later one.
// A repeat run restates facts and the log keeps one of each; a fact never
// extracted is not recoverable at all.
package commit

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/archive"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Result is what one commit did: the memories it wrote, and the session as it
// stands afterwards.
//
// The session is the session's state and not this run's. A commit that found
// the cold half already moved aside answers with the summary that was already
// there, which is the true statement about the conversation — and a caller
// comparing Archived against what it saw last is asking whether the
// conversation has grown, not whether an endpoint was called.
type Result struct {
	Memories []memory.Memory
	Session  session.Session
}

// Committer reads a session, writes what is worth remembering from it, and
// moves aside what will not be read again.
type Committer struct {
	sessions *session.Store
	memories *memory.Store
	extract  llm.FactExtractor
	archiver *archive.Archiver
	keep     int
}

// New returns the committer reading sessions from sessions, writing memories to
// memories, extracting through extract, and reducing through archiver while
// keeping the last keep turns of a conversation as they were said. The archiver
// reads and writes the same sessions.
//
// A nil extractor or a nil archiver is refused. Nil is what the gateway answers
// when nothing in the chain serves a capability, and a committer built on one
// would accept every session and fail on each one, which is a build that looks
// wired and remembers nothing.
//
// Keeping no turns hot is refused rather than read as "reduce everything": it
// is the setting that hands a caller a summary where the turn it is answering
// should be.
func New(sessions *session.Store, memories *memory.Store, extract llm.FactExtractor, archiver *archive.Archiver, keep int) (*Committer, error) {
	switch {
	case sessions == nil || memories == nil:
		return nil, fmt.Errorf("committer needs a session store and a memory store: %w", fs.ErrInvalid)
	case extract == nil:
		return nil, fmt.Errorf("committer needs an extractor; no configured endpoint extracts: %w", fs.ErrInvalid)
	case archiver == nil:
		return nil, fmt.Errorf("committer needs an archiver; no configured endpoint summarises: %w", fs.ErrInvalid)
	case keep < 1:
		return nil, fmt.Errorf("committer keeps %d turns hot, leaving a session with no live half: %w", keep, fs.ErrInvalid)
	}
	return &Committer{sessions: sessions, memories: memories, extract: extract, archiver: archiver, keep: keep}, nil
}

// Commit reads the session named name in s, extracts what is worth remembering
// from it into the workspace's memories, and moves everything but the last few
// turns into the session's archive behind a summary. It answers with both.
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
// grown finds the hot half already within the policy and calls no endpoint.
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

	reduced, err := c.archiver.Archive(ctx, s, name, c.keep)
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}
	return Result{Memories: written, Session: reduced}, nil
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
