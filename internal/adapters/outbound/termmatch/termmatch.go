// Package termmatch ranks by shared vocabulary, weighting a word by how few of
// the things being ranked use it.
//
// It is the laptop's ranker: no model, no network, nothing to start. The second
// implementation is the one this exists to be replaced by — one that embeds the
// question and the candidates through the retrieval layer's provider — and
// neither caller can tell them apart, which is the whole reason the ranking
// sits behind a port.
//
// Two ports are served by one scoring. Matcher ranks a library's cards, Ranker
// ranks the entries under a search prefix, and the difference between them is
// which text the vocabulary is taken from: neither the weights nor the ordering
// care what the text was.
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

// Matcher ranks a task against a library's cards. It holds nothing: the weights
// come from the cards it is handed, so a library that gains a document ranks
// differently on the next call with no index to rebuild.
type Matcher struct{}

// New returns the matcher.
func New() Matcher { return Matcher{} }

// Rank scores each card by how much of the task's distinctive vocabulary it
// uses, and returns the ones that use any of it, best first.
//
// A card is its name and its description: what a document is called is part of
// what it says it is for.
func (Matcher) Rank(_ context.Context, task string, cards []library.Card) ([]library.Match, error) {
	subjects := make([]subject, len(cards))
	for i, card := range cards {
		subjects[i] = subject{name: card.Name, text: card.Name + " " + card.Description}
	}

	matches := make([]library.Match, 0, len(cards))
	for _, ranked := range rank(task, subjects) {
		matches = append(matches, library.Match{Card: cards[ranked.index], Score: ranked.score})
	}
	return matches, nil
}

// subject is one thing to be ranked: the text its vocabulary is taken from, and
// the name that breaks a tie between two that cover the same words.
type subject struct {
	name string
	text string
}

// scored is where a subject stood in the slice it arrived in, and how relevant
// it is. An index rather than the subject itself, so the caller hands back the
// value it was given rather than one rebuilt from a copy.
type scored struct {
	index int
	score float64
}

// rank scores each subject by how much of the query's distinctive vocabulary it
// uses, and returns the ones that use any of it, best first.
//
// A word's weight falls as more subjects use it, so a word every subject shares
// separates nothing and a word one subject has decides the order. The weight
// never reaches zero, which is what keeps a collection of one answerable: with
// one subject, every word is both universal and unique.
//
// The score is the share of the query's total weight the subject covers, so it
// sits in (0,1] and one that covers the whole query scores 1. Subjects that
// cover none of it are not returned: they are not relevant, and saying so by
// putting them last is how everything ends up in a prompt.
func rank(query string, subjects []subject) []scored {
	wanted := words(query)
	if len(wanted) == 0 || len(subjects) == 0 {
		return nil
	}

	vocabularies := make([]map[string]struct{}, len(subjects))
	for i, s := range subjects {
		vocabularies[i] = words(s.text)
	}

	total := 0.0
	weights := make(map[string]float64, len(wanted))
	for word := range wanted {
		weights[word] = weigh(word, vocabularies)
		total += weights[word]
	}

	ranked := make([]scored, 0, len(subjects))
	for i := range subjects {
		score := 0.0
		for word, weight := range weights {
			if _, used := vocabularies[i][word]; used {
				score += weight
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{index: i, score: score / total})
		}
	}

	// The name breaks a tie, so two subjects that cover the same words come
	// back in the same order every time rather than in map order.
	slices.SortFunc(ranked, func(a, b scored) int {
		if a.score != b.score {
			return cmpDescending(a.score, b.score)
		}
		return strings.Compare(subjects[a.index].name, subjects[b.index].name)
	})
	return ranked
}

// weigh is how much one word says about which subject is meant: the fewer
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
// digits, the short ones dropped, each counted once. A word twice makes the
// text longer, not the subject more relevant.
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
