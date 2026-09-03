// Package commit is what happens to a conversation when it is over: the
// transcript is read, what is worth remembering is extracted from it, and the
// result is written to the workspace's memories.
//
// It is the stage that turns a record of what was said into claims that stand
// on their own. The transcript is not touched — it is append-only and this
// package only reads it — so a commit can be run again over the same session
// and the session is the same session afterwards.
//
// Extraction is a model call and goes through the gateway, so no stage here
// holds a provider client and which endpoint reads the transcript is a line of
// config. A build with no extracting endpoint has no committer, which it finds
// out where it wires the pipeline rather than on the first conversation.
package commit

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Committer reads a session and writes what it is worth remembering.
type Committer struct {
	sessions *session.Store
	memories *memory.Store
	extract  llm.FactExtractor
}

// New returns the committer reading sessions from sessions, writing memories to
// memories, and extracting through extract.
//
// A nil extractor is refused. It is what the gateway answers when nothing in
// the chain extracts, and a committer built on it would accept every session
// and fail on each one, which is a build that looks wired and remembers
// nothing.
func New(sessions *session.Store, memories *memory.Store, extract llm.FactExtractor) (*Committer, error) {
	switch {
	case sessions == nil || memories == nil:
		return nil, fmt.Errorf("committer needs a session store and a memory store: %w", fs.ErrInvalid)
	case extract == nil:
		return nil, fmt.Errorf("committer needs an extractor; no configured endpoint extracts: %w", fs.ErrInvalid)
	}
	return &Committer{sessions: sessions, memories: memories, extract: extract}, nil
}

// Commit reads the session named name in s, extracts what is worth remembering
// from it, and adds it to the workspace's memories. It answers with what it
// wrote.
//
// A session nobody created reports fs.ErrNotExist, from the read: a commit of a
// misspelled name that quietly wrote nothing looks exactly like a conversation
// with nothing in it.
//
// A conversation with nothing worth remembering commits nothing and is not a
// failure. Committing the same session twice writes its facts twice — nothing
// here compares a fact against what is already stored, which is E8.5's job.
func (c *Committer) Commit(ctx context.Context, s workspace.Scope, name string) ([]memory.Memory, error) {
	transcript, err := c.sessions.Get(ctx, s, name)
	if err != nil {
		return nil, fmt.Errorf("commit session %q: %w", name, err)
	}

	facts, err := c.extract.Extract(ctx, turnsOf(transcript.Messages))
	if err != nil {
		return nil, fmt.Errorf("commit session %q: %w", name, err)
	}

	written, err := c.memories.Add(ctx, s, name, factsOf(facts))
	if err != nil {
		return nil, fmt.Errorf("commit session %q: %w", name, err)
	}
	return written, nil
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
