package memory

import "strings"

// claim is what two facts have to share to be the same thing said twice: the
// category the fact was filed under, and what it says.
//
// Both are read as text rather than as bytes, so two facts differing only in
// capitalisation or spacing are one claim written twice. Anything past that —
// different punctuation, different wording, a paraphrase — is deliberately a
// different claim here. Deciding whether two sentences mean the same thing is a
// model's judgement and belongs to consolidation and reconciliation; a filter
// standing in front of every write judges only whether something was restated.
//
// The category is half the identity because it is what a fact was filed under.
// "use tabs" as a preference and "use tabs" as an instruction are a habit and
// an order, and a workspace that folded them together would answer a request
// for its instructions with something the user never told it to do.
type claim struct {
	kind string
	body string
}

// claimOf reads the claim a fact makes.
func claimOf(f Fact) claim {
	return claim{kind: fold(f.Kind), body: fold(f.Body)}
}

// fold reads text as what it says rather than as how it was typed. Runs of
// space are collapsed rather than removed, so "prefers tabs" and "prefers  tabs"
// are one claim and "preferstabs" is not.
func fold(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// fresh returns the facts in the batch that restate nothing, in the order they
// were given: neither something in believed nor an earlier fact in the same
// batch.
//
// The batch is filtered against one index of claims built once, so a commit
// pays one pass over what the workspace holds rather than one comparison per
// fact — and because the index is keyed by category, a fact is only ever
// weighed against the memories filed under its own.
//
// Only what is believed is in the index. A memory something superseded is a
// belief the workspace gave up, so stating it again is that belief returning
// and is written; folding it into the memory that no longer stands would make
// the log say the workspace never changed its mind.
//
// The first statement of a claim is the one that survives, so a memory names
// the conversation the claim was first made in rather than the last one to
// repeat it.
func fresh(believed []Memory, facts []Fact) []Fact {
	made := make(map[claim]bool, len(believed)+len(facts))
	for _, m := range believed {
		made[claimOf(m.Fact)] = true
	}
	kept := make([]Fact, 0, len(facts))
	for _, fact := range facts {
		c := claimOf(fact)
		if made[c] {
			continue
		}
		made[c] = true
		kept = append(kept, fact)
	}
	return kept
}
