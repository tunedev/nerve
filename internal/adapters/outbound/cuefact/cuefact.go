// Package cuefact reads what is worth remembering out of a conversation by the
// phrases people use when they say something worth remembering.
//
// It is the laptop's provider: no model, no network, nothing to start. It
// reaches a caller the way every model endpoint does, through the gateway's
// chain, so a build that puts a hosted model in front of it changes a line of
// config, and the commit stage holding the port cannot tell the two apart.
//
// Be plain about what this is. It reads cues, not meaning. Every fact it
// answers with is a sentence the user already said, typed by the phrase that
// caught it and ranked by how firmly it was said — so it finds "always run the
// tests" and misses the same instruction stated in a way nobody spelled out
// here. It is the honest floor under the pipeline rather than a small model.
//
// Only what the user said is extracted. The assistant's own output is read past
// entirely: a preference the user stated is a fact about the user, and the same
// sentence in a reply is a fact about nothing, and there is no worse memory
// than a model's own suggestion stored as something a person asked for.
package cuefact

import (
	"context"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/core/llm"
)

// Role is whose turns are read. It is the role a session's caller writes for
// the person, and it is a setting because the vocabulary is the caller's.
const Role = "user"

// cue is a phrase that says a sentence is worth keeping, with what sort of fact
// it makes.
//
// The phrases are matched against the sentence lowercased, so a cue is written
// lowercase here. A sentence caught by more than one cue takes the first in
// this order, which is why the strongest kinds are written first: "never change
// my name" is an instruction before it is anything about a name.
type cue struct {
	phrase   string
	kind     string
	priority llm.Priority
}

// cues is the whole of what this extractor knows. Adding a kind is a line here.
//
// An instruction outranks the rest because it is what a caller keeps when it
// keeps one thing: getting a preference wrong reads oddly, and ignoring "never
// force push" breaks something. Identity is next because it is the thing a
// conversation is least likely to restate. What someone merely likes or decided
// once is ordinary.
var cues = []cue{
	{"never ", "instruction", llm.High},
	{"always ", "instruction", llm.High},
	{"don't ", "instruction", llm.High},
	{"do not ", "instruction", llm.High},
	{"make sure ", "instruction", llm.High},
	{"my name is ", "identity", llm.High},
	{"i work ", "identity", llm.High},
	{"i live ", "identity", llm.High},
	{"i am ", "identity", llm.Normal},
	{"i'm ", "identity", llm.Normal},
	{"i prefer ", "preference", llm.Normal},
	{"i like ", "preference", llm.Normal},
	{"i'd rather ", "preference", llm.Normal},
	{"i hate ", "preference", llm.Normal},
	{"we decided ", "decision", llm.Normal},
	{"we're going to ", "decision", llm.Normal},
	{"let's use ", "decision", llm.Normal},
}

// hedges are the words people say when they are not committing to what they are
// saying. A hedged sentence is still worth keeping and is worth dropping first,
// so it comes back one rung down.
//
// This is what stops priority being a second spelling of the kind. "Never force
// push" and "maybe never force push" are the same kind of claim and not the
// same claim.
var hedges = []string{"maybe ", "might ", "i think ", "probably ", "sometimes ", "perhaps ", "not sure "}

// Extractor reads facts out of a transcript by cue. It holds nothing: the cues
// are the package's, so there is no index and nothing to invalidate.
type Extractor struct{ role string }

// New returns the extractor reading the turns of role. A build whose sessions
// call the person something other than "user" says so here rather than
// everywhere below.
func New(role string) Extractor { return Extractor{role: role} }

// Extract returns what the person said that is worth remembering, in the order
// they said it.
//
// A conversation with no cue in it extracts to nothing, which is an answer: a
// transcript is mostly not facts, and an extractor that always found one would
// fill a workspace with sentences nobody will want back.
//
// A sentence is answered once even when several cues catch it. The same
// sentence stored twice under two kinds is one fact a caller now has to
// reconcile against itself.
func (e Extractor) Extract(_ context.Context, turns []llm.Turn) ([]llm.Fact, error) {
	var facts []llm.Fact
	for _, turn := range turns {
		if turn.Role != e.role {
			continue
		}
		for _, sentence := range termsum.Sentences(turn.Body) {
			if fact, found := factOf(sentence); found {
				facts = append(facts, fact)
			}
		}
	}
	return facts, nil
}

// factOf is the fact a sentence makes, and whether it makes one at all.
func factOf(sentence string) (llm.Fact, bool) {
	said := strings.ToLower(sentence)
	for _, c := range cues {
		if !strings.Contains(said, c.phrase) {
			continue
		}
		return llm.Fact{Kind: c.kind, Body: sentence, Priority: ranked(said, c.priority)}, true
	}
	return llm.Fact{}, false
}

// ranked drops a hedged sentence one rung, and never below the bottom of the
// scale: a hedged aside is still something the person said.
func ranked(said string, priority llm.Priority) llm.Priority {
	for _, hedge := range hedges {
		if strings.Contains(said, hedge) && priority > llm.Low {
			return priority - 1
		}
	}
	return priority
}
