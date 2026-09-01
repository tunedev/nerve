// Package termsum says what a document is about by picking the sentences that
// carry most of its vocabulary, weighting a word by how much of the document it
// runs through.
//
// It is the laptop's summariser: no model, no network, nothing to start. The
// second implementation is the one this exists to be replaced by — a model
// behind the retrieval layer's gateway, which writes sentences the document
// never contained — and the stage that holds the port cannot tell them apart.
//
// Be plain about what this is: it is extractive, not abstractive. Every
// sentence it returns is one the document already had, and a document that
// never states its own subject in one sentence has no abstract here for the
// summariser to find.
package termsum

import (
	"context"
	"math"
	"slices"
	"strings"
	"unicode"
)

// Words shorter than this are dropped, for the reason termmatch drops them: two
// letters is what a domain's acronyms are, and one letter is never one of those.
const shortestWord = 2

// Summariser picks a document's own sentences. It holds nothing: the weights
// come from the document it is handed, so there is no index and nothing to
// invalidate.
type Summariser struct{}

// New returns the summariser.
func New() Summariser { return Summariser{} }

// Summarise returns at most sentences of text, in the order the document had
// them, chosen by how much of the document's vocabulary each one carries.
//
// The budget is a ceiling. A document with fewer sentences than the rung allows
// comes back whole, which is what makes a short document's abstract, overview
// and detail the same words rather than three truncations of it.
//
// The sentences come back one to a line. Two sentences the document did not put
// together are not put together here either: run into a paragraph they would
// read as prose the document never wrote.
func (Summariser) Summarise(_ context.Context, text string, sentences int) (string, error) {
	said := split(text)
	if sentences < 1 || len(said) == 0 {
		return "", nil
	}
	if len(said) <= sentences {
		return strings.Join(said, "\n"), nil
	}

	scores := score(said)
	order := make([]int, len(said))
	for i := range order {
		order[i] = i
	}
	// Position breaks a tie, so two sentences carrying the same weight come
	// back in the order the document had them rather than in whatever order
	// the sort left them.
	slices.SortStableFunc(order, func(a, b int) int {
		if scores[a] != scores[b] {
			return cmpDescending(scores[a], scores[b])
		}
		return a - b
	})

	chosen := order[:sentences]
	slices.Sort(chosen)
	picked := make([]string, len(chosen))
	for i, at := range chosen {
		picked[i] = said[at]
	}
	return strings.Join(picked, "\n"), nil
}

// score is how much of the document each sentence carries: the weight of its
// distinct words, added up. A word said twice in one sentence is one word —
// repeating yourself makes a sentence longer, not more about the document.
//
// Longer sentences score higher, which is the bias this takes on knowingly: a
// sentence that carries more of the vocabulary is more of the document, and a
// summary that preferred short sentences would answer with the document's
// asides.
func score(said []string) []float64 {
	vocabularies := make([]map[string]int, len(said))
	frequency := map[string]int{}
	holding := map[string]int{}
	for i, sentence := range said {
		vocabularies[i] = count(sentence)
		for word, times := range vocabularies[i] {
			frequency[word] += times
			holding[word]++
		}
	}

	scores := make([]float64, len(said))
	for i := range said {
		for word := range vocabularies[i] {
			scores[i] += weigh(frequency[word], holding[word], len(said))
		}
	}
	return scores
}

// weigh is how much one word says about what a document is about.
//
// It is how often the word is said, discounted by how much of the document says
// it. A word in every sentence weighs nothing at all, which is what happens to
// "the" without a list of words to ignore; a word said once somewhere is a
// detail rather than a subject, and is worth little because it is said once.
// What weighs most is the word said often and not everywhere, which is the word
// the document is about.
func weigh(frequency, holding, sentences int) float64 {
	return float64(frequency) * math.Log(float64(sentences)/float64(holding))
}

// count is the words of one sentence and how often each is said: lowercased
// runs of letters and digits, the short ones dropped.
func count(sentence string) map[string]int {
	found := map[string]int{}
	for _, word := range strings.FieldsFunc(strings.ToLower(sentence), notWord) {
		if len(word) >= shortestWord {
			found[word]++
		}
	}
	return found
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// split is the sentences a document is made of.
//
// A sentence ends at a full stop, a question mark or an exclamation — or at the
// end of the paragraph, because a heading and a list item are sentences that
// never finish. A single newline inside a paragraph is a place the text was
// wrapped and not a place a sentence ended, so it becomes a space; a blank line
// is a paragraph and does end one.
//
// It is a reader of prose and it knows nothing of abbreviations: "e.g. the
// pumps" is two sentences here. A number is not, because a stop with no space
// after it has not ended anything.
func split(text string) []string {
	var said []string
	for _, block := range paragraphs(text) {
		said = append(said, sentencesOf(block)...)
	}
	return said
}

// paragraphs is the text as its paragraphs, each one line with its wrapping
// undone.
func paragraphs(text string) []string {
	var found []string
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(lines) > 0 {
				found = append(found, strings.Join(lines, " "))
				lines = nil
			}
			continue
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	if len(lines) > 0 {
		found = append(found, strings.Join(lines, " "))
	}
	return found
}

// sentencesOf cuts one paragraph at its terminators. A run of them is one
// ending — "..." and "?!" finish a sentence once.
func sentencesOf(block string) []string {
	var found []string
	start := 0
	for i := 0; i < len(block); i++ {
		if !terminator(block[i]) {
			continue
		}
		end := i + 1
		for end < len(block) && terminator(block[end]) {
			end++
		}
		i = end - 1
		if end < len(block) && block[end] != ' ' {
			continue
		}
		if sentence := strings.TrimSpace(block[start:end]); sentence != "" {
			found = append(found, sentence)
		}
		start = end
	}
	if rest := strings.TrimSpace(block[start:]); rest != "" {
		found = append(found, rest)
	}
	return found
}

func terminator(c byte) bool { return c == '.' || c == '!' || c == '?' }

// cmpDescending orders the higher score first.
func cmpDescending(a, b float64) int {
	if a > b {
		return -1
	}
	return 1
}
