// Package commit is what happens to a conversation when it is over: the
// transcript is read, what is worth remembering is extracted from it, the half
// nobody will read again is reduced to a summary, and the result is written to
// the workspace.
//
// It is the stage that turns a record of what was said into claims that stand
// on their own, and the rest of it into a paragraph. Extraction writes
// memories; compression writes a digest beside the transcript, covering
// everything but the most recent turns. No message is rewritten and none is
// removed — the transcript is append-only and this package only reads it — so a
// commit can be run again over the same session and the session is the same
// session afterwards.
//
// Both stages are model calls and both go through the gateway, so no stage here
// holds a provider client and which endpoint reads the transcript is a line of
// config. A build with no extracting or no summarising endpoint has no
// committer, which it finds out where it wires the pipeline rather than on the
// first conversation.
//
// Nothing is written until both endpoints have answered, and the digest is
// written before the memories. A commit interrupted at that point has recorded
// a reduction it can make again to the same coverage; the other order would
// leave facts that a second run writes a second time.
package commit

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Compression is where a transcript's cold half ends and how much of a summary
// stands for it. Both are operator's numbers: how much conversation a turn is
// built from, and how long the summary of the rest is allowed to be.
type Compression struct {
	// Keep is how many of the most recent messages stay as they were said.
	// Everything before them is cold and is what the summary covers.
	Keep int
	// Sentences is the ceiling on the summary. It is a ceiling and not a
	// quota: a short conversation says less.
	Sentences int
}

// valid returns fs.ErrInvalid for a policy that compresses nothing usable.
//
// Keeping nothing hot is refused rather than read as "summarise everything": a
// transcript with no hot half has no cold half either — there is nothing the
// summary is a reduction of — and it is the setting that would hand a caller a
// paragraph where the turn it is answering should be. A budget of no sentences
// is a summary that says nothing, which the store refuses on every commit.
func (c Compression) valid() error {
	switch {
	case c.Keep < 1:
		return fmt.Errorf("compression keeps %d messages hot; a transcript with no recent half has no cold one: %w", c.Keep, fs.ErrInvalid)
	case c.Sentences < 1:
		return fmt.Errorf("compression allows %d sentences, which is no summary: %w", c.Sentences, fs.ErrInvalid)
	}
	return nil
}

// Result is what one commit did: the memories it wrote, and the digest the
// session carries afterwards.
//
// The digest is the session's and not this run's. A commit that found the cold
// half already covered answers with the digest that was already there, which is
// the true statement about the session — and a caller comparing Through against
// what it saw last is asking whether the conversation has grown, not whether an
// endpoint was called.
type Result struct {
	Memories []memory.Memory
	Digest   session.Digest
}

// Committer reads a session, writes what it is worth remembering, and reduces
// what will not be read again.
type Committer struct {
	sessions  *session.Store
	memories  *memory.Store
	extract   llm.FactExtractor
	summarise llm.Summariser
	policy    Compression
}

// New returns the committer reading sessions from sessions, writing memories to
// memories, extracting through extract and compressing through summarise under
// policy.
//
// A nil extractor or a nil summariser is refused. Nil is what the gateway
// answers when nothing in the chain serves the capability, and a committer
// built on one would accept every session and fail on each one, which is a
// build that looks wired and remembers nothing.
func New(sessions *session.Store, memories *memory.Store, extract llm.FactExtractor, summarise llm.Summariser, policy Compression) (*Committer, error) {
	switch {
	case sessions == nil || memories == nil:
		return nil, fmt.Errorf("committer needs a session store and a memory store: %w", fs.ErrInvalid)
	case extract == nil:
		return nil, fmt.Errorf("committer needs an extractor; no configured endpoint extracts: %w", fs.ErrInvalid)
	case summarise == nil:
		return nil, fmt.Errorf("committer needs a summariser; no configured endpoint summarises: %w", fs.ErrInvalid)
	}
	if err := policy.valid(); err != nil {
		return nil, fmt.Errorf("committer: %w", err)
	}
	return &Committer{sessions: sessions, memories: memories, extract: extract, summarise: summarise, policy: policy}, nil
}

// Commit reads the session named name in s, extracts what is worth remembering
// from it into the workspace's memories, and reduces its cold half to a digest.
// It answers with both.
//
// A session nobody created reports fs.ErrNotExist, from the read: a commit of a
// misspelled name that quietly wrote nothing looks exactly like a conversation
// with nothing in it.
//
// A conversation with nothing worth remembering commits nothing and is not a
// failure. Committing the same session twice writes its facts twice — nothing
// here compares a fact against what is already stored, which is E8.5's job.
// Compression is not like that: a second commit over a conversation that has
// not grown finds the cold half already covered and calls no endpoint.
func (c *Committer) Commit(ctx context.Context, s workspace.Scope, name string) (Result, error) {
	transcript, err := c.sessions.Get(ctx, s, name)
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}

	facts, err := c.extract.Extract(ctx, turnsOf(transcript.Messages))
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}
	summary, through, err := c.reduce(ctx, transcript)
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}

	digest := transcript.Digest
	if through > 0 {
		if digest, err = c.sessions.Compress(ctx, s, name, summary, through); err != nil {
			return Result{}, fmt.Errorf("commit session %q: %w", name, err)
		}
	}
	written, err := c.memories.Add(ctx, s, name, factsOf(facts))
	if err != nil {
		return Result{}, fmt.Errorf("commit session %q: %w", name, err)
	}
	return Result{Memories: written, Digest: digest}, nil
}

// reduce summarises the transcript's cold half, and answers with the sequence
// the summary covers through. A zero sequence is nothing to compress, which is
// a conversation shorter than the hot half or one already covered.
//
// The whole cold half is summarised each time rather than the new part being
// folded into the standing digest. A summary of a summary drifts from the
// conversation with every commit, and there is no way back to the turns it came
// from; re-reading the cold half costs a model call and says what the
// conversation actually said.
func (c *Committer) reduce(ctx context.Context, transcript session.Transcript) (string, int, error) {
	cold := coldOf(transcript.Messages, c.policy.Keep)
	if len(cold) == 0 || cold[len(cold)-1].Seq <= transcript.Digest.Through {
		return "", 0, nil
	}
	summary, err := c.summarise.Summarise(ctx, conversationOf(cold), c.policy.Sentences)
	if err != nil {
		return "", 0, err
	}
	return summary, cold[len(cold)-1].Seq, nil
}

// coldOf is the half of a conversation nobody will read again: everything
// before the keep most recent messages. A conversation no longer than the hot
// half is cold nowhere.
func coldOf(messages []session.Message, keep int) []session.Message {
	if len(messages) <= keep {
		return nil
	}
	return messages[:len(messages)-keep]
}

// conversationOf renders turns as the document a summariser reads.
//
// Each turn is named by its speaker and stands as its own paragraph, because a
// summariser reading prose takes a blank line for the end of a thought and a
// single newline for a wrapped one: run together, two turns would be summarised
// as one sentence neither of them said.
func conversationOf(messages []session.Message) string {
	said := make([]string, len(messages))
	for i, message := range messages {
		said[i] = message.Role + ": " + message.Body
	}
	return strings.Join(said, "\n\n")
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
