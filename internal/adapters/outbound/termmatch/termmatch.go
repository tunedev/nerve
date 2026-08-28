// Package termmatch ranks a library's documents by the vocabulary their cards
// share with a task, weighting a word by how few cards use it.
//
// It is the laptop's matcher: no model, no network, nothing to start. The
// second implementation is the one this exists to be replaced by — a matcher
// that embeds the task and the cards through the retrieval layer's provider —
// and the registry cannot tell them apart, which is the whole reason the
// ranking sits behind a port instead of inside the registry.
package termmatch

import (
	"context"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/tunedev/nerve/internal/core/library"
)

// Words shorter than this are dropped. Two letters stay because that is what a
// domain's acronyms are — "cv", "id", "os" — and dropping them loses the word
// the task was actually about. One letter is never one of those.
const shortestWord = 2

// Matcher ranks a task against cards. It holds nothing: the weights come from
// the cards it is handed, so a library that gains a document ranks differently
// on the next call with no index to rebuild.
type Matcher struct{}

// New returns the matcher.
func New() Matcher { return Matcher{} }

// Rank scores each card by how much of the task's distinctive vocabulary it
// uses, and returns the ones that use any of it, best first.
//
// A word's weight falls as more cards use it, so a word every card shares
// separates nothing and a word one card has decides the order. The weight never
// reaches zero, which is what keeps a library of one document answerable: with one
// card, every word is both universal and unique.
//
// The score is the share of the task's total weight the card covers, so it sits
// in (0,1] and a card that covers the whole task scores 1. Cards that cover
// none of it are not returned: they are not relevant, and saying so by putting
// them last is how everything ends up in a prompt.
func (Matcher) Rank(_ context.Context, task string, cards []library.Card) ([]library.Match, error) {
	wanted := words(task)
	if len(wanted) == 0 || len(cards) == 0 {
		return nil, nil
	}

	vocabularies := make([]map[string]struct{}, len(cards))
	for i, card := range cards {
		vocabularies[i] = words(card.Name + " " + card.Description)
	}

	total := 0.0
	weights := make(map[string]float64, len(wanted))
	for word := range wanted {
		weights[word] = weigh(word, vocabularies)
		total += weights[word]
	}

	matches := make([]library.Match, 0, len(cards))
	for i, card := range cards {
		score := 0.0
		for word, weight := range weights {
			if _, used := vocabularies[i][word]; used {
				score += weight
			}
		}
		if score > 0 {
			matches = append(matches, library.Match{Card: card, Score: score / total})
		}
	}

	// Name breaks a tie, so two cards that cover the same words come back in
	// the same order every time rather than in map order.
	slices.SortFunc(matches, func(a, b library.Match) int {
		if a.Score != b.Score {
			return cmpDescending(a.Score, b.Score)
		}
		return strings.Compare(a.Card.Name, b.Card.Name)
	})
	return matches, nil
}

// weigh is how much one word says about which card is meant: the fewer
// vocabularies hold it, the more.
func weigh(word string, vocabularies []map[string]struct{}) float64 {
	holding := 0
	for _, vocabulary := range vocabularies {
		if _, used := vocabulary[word]; used {
			holding++
		}
	}
	if holding == 0 {
		return 0
	}
	return math.Log(1 + float64(len(vocabularies))/float64(holding))
}

// words is the vocabulary of a piece of text: lowercased runs of letters and
// digits, the short ones dropped, each counted once. A word twice in a
// description makes the description longer, not the document more relevant.
func words(text string) map[string]struct{} {
	found := map[string]struct{}{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), notWord) {
		if len(word) >= shortestWord {
			found[word] = struct{}{}
		}
	}
	return found
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// cmpDescending orders the higher score first.
func cmpDescending(a, b float64) int {
	if a > b {
		return -1
	}
	return 1
}
