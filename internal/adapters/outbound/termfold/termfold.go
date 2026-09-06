// Package termfold merges the facts a workspace believes by the vocabulary they
// share, within the category they were filed under.
//
// It is the laptop's consolidator: no model, no network, nothing to start. It
// reaches a caller the way every model endpoint does, through the gateway's
// chain, so a build that puts a hosted model in front of it changes a line of
// config, and the commit stage holding the port cannot tell the two apart.
//
// Be plain about what this is. It reads shared words, not meaning, so what it
// merges is a claim and the same claim said again with more of it: "prefers
// tabs" and "prefers tabs in Go" are one narrative here. A paraphrase is not.
// "likes tabs" says what "prefers tabs" says and shares one word out of three
// with it, which is the same overlap "prefers dark mode" has — this endpoint
// cannot keep the first and drop the second, so it drops both. It is the honest
// floor under consolidation rather than a small model.
//
// The narrative it writes is the merged facts' own sentences, joined in the
// order they were written. It merges; it does not rewrite. A build that wants a
// sentence the workspace never said needs an endpoint that can write one.
package termfold

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"unicode"

	"github.com/tunedev/nerve/internal/core/llm"
)

// Words shorter than this are dropped, for the reason termsum drops them: two
// letters is what a domain's acronyms are, and one letter is never one of those.
const shortestWord = 2

// Consolidator merges facts that share vocabulary. It holds nothing but its
// threshold: what counts as related comes from the facts it is handed, so a
// workspace that gains a memory merges differently on the next call with no
// index to rebuild.
type Consolidator struct{ share float64 }

// New returns a consolidator merging two facts that share at least share of the
// words between them.
//
// A threshold at or below zero merges every fact in a category into one
// narrative, and one above one merges nothing at all. Both are refused: the
// first is a workspace that answers every question with its whole history of
// preferences, and the second is an endpoint that looks wired and merges
// nothing.
func New(share float64) (Consolidator, error) {
	if share <= 0 || share > 1 {
		return Consolidator{}, fmt.Errorf("a consolidator merging at %v shares either merges everything or nothing: %w", share, fs.ErrInvalid)
	}
	return Consolidator{share: share}, nil
}

// Consolidate answers with the groups of facts that share enough vocabulary to
// be one claim, each with the sentences they say joined into one.
//
// Only facts filed under one category are ever merged. The category is what a
// fact was filed under, and a workspace asked for its instructions must not
// answer with a preference nobody told it to keep — the same line the stage
// above holds, held here so this endpoint never proposes a merge that would be
// declined.
//
// Relatedness carries: a fact related to a second, and that second to a third,
// puts all three in one narrative even where the first and third share nothing.
// A claim narrowed twice is one claim, and cutting the chain would answer with
// two narratives that overlap.
//
// Nothing here fails. There is no endpoint to be down and no model to be
// missing; the error is in the signature because the port is a remote thing's.
func (c Consolidator) Consolidate(_ context.Context, facts []llm.Fact) ([]llm.Narrative, error) {
	vocabularies := make([]map[string]struct{}, len(facts))
	for i, fact := range facts {
		vocabularies[i] = words(fact.Body)
	}

	var narratives []llm.Narrative
	for _, group := range group(facts, vocabularies, c.share) {
		said := make([]string, len(group))
		for i, at := range group {
			said[i] = facts[at].Body
		}
		narratives = append(narratives, llm.Narrative{Facts: group, Body: strings.Join(said, "; ")})
	}
	return narratives, nil
}

// group is the positions of the facts that merge, one slice per narrative,
// ascending within a group and in the order the groups first appear.
//
// A fact joins the first group it is related to and pulls that group's later
// matches in with it, which is what makes relatedness carry without a second
// pass.
func group(facts []llm.Fact, vocabularies []map[string]struct{}, share float64) [][]int {
	taken := make([]bool, len(facts))
	var groups [][]int
	for i := range facts {
		if taken[i] {
			continue
		}
		taken[i] = true
		merged := []int{i}
		for at := 0; at < len(merged); at++ {
			for j := range facts {
				if taken[j] || facts[j].Kind != facts[i].Kind || !related(vocabularies[merged[at]], vocabularies[j], share) {
					continue
				}
				taken[j] = true
				merged = append(merged, j)
			}
		}
		if len(merged) > 1 {
			groups = append(groups, merged)
		}
	}
	return groups
}

// related reports whether two facts share enough of their vocabulary to be one
// claim: the share of the words in either that are in both.
//
// It is a share rather than a count because a count would make a long sentence
// related to everything — a fact naming ten things shares one word with most of
// what a workspace believes, and merging on that would fold a category into one
// paragraph.
//
// A fact with no words of its own is related to nothing. There is nothing to
// count, and the alternative is treating "it is" as the claim every other one
// narrows.
func related(a, b map[string]struct{}, share float64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	both := 0
	for word := range a {
		if _, shared := b[word]; shared {
			both++
		}
	}
	return float64(both)/float64(len(a)+len(b)-both) >= share
}

// words is the distinct words of a sentence: lowercased runs of letters and
// digits, the short ones dropped.
func words(sentence string) map[string]struct{} {
	found := map[string]struct{}{}
	for _, word := range strings.FieldsFunc(strings.ToLower(sentence), notWord) {
		if len(word) >= shortestWord {
			found[word] = struct{}{}
		}
	}
	return found
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
