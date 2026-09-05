// Package termvec turns text into a vector by counting its words into a fixed
// number of buckets, hashing each word to the one it lands in.
//
// It is the laptop's embedder: no model, no network, nothing to start. termsum
// plays that part for summarisation and this is its counterpart, so a build
// with no Ollama running still has something behind gateway.AsEmbedder() and a
// vectorfs mount that would otherwise be refused comes up.
//
// Be plain about what this is. It is a bag of words: two texts that share
// vocabulary are close and two that share none are not, which is enough to rank
// documents and is not what a trained model does. It knows no synonyms, no word
// order and no meaning, and two words that hash to one bucket are one word to
// it.
//
// It is not a fallback for a model. Vectors are comparable only with vectors
// from the embedder that made them, so a chain listing this behind Ollama would
// fill one index from two vector spaces the first time Ollama faltered — the
// trap llm.Embedder names as the operator's line to hold. This is the endpoint
// for a build that has no model, not the second endpoint for a build that has
// one.
package termvec

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"slices"
	"strings"
	"unicode"
)

// Words shorter than this are dropped, for the reason termsum drops them: two
// letters is what a domain's acronyms are, and one letter is never one of those.
const shortestWord = 2

// Embedder counts a text's words into buckets. It holds nothing: which bucket a
// word lands in is the hash of the word, so two runs of one build answer the
// same text the same way and there is no index to rebuild.
type Embedder struct{ width int }

// New returns an embedder answering with vectors of width dimensions. A width
// under one has no bucket to count into, so it is refused rather than answering
// every text with a vector of nothing.
func New(width int) (Embedder, error) {
	if width < 1 {
		return Embedder{}, fmt.Errorf("an embedder of %d dimensions has no bucket to count a word into: %w", width, fs.ErrInvalid)
	}
	return Embedder{width: width}, nil
}

// Width is how many dimensions every vector has. It is readable because a mount
// holding these vectors was sized by config and this is the number that config
// has to have said.
func (e Embedder) Width() int { return e.width }

// Embed answers with one vector per text, in the order the texts were given.
//
// Nothing here fails: there is no endpoint to be down and no model to be
// missing. The error is in the signature because the port is a remote thing's,
// and this is the implementation that never needs it.
func (e Embedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vectors[i] = e.vector(text)
	}
	return vectors, nil
}

// vector counts one text's words into their buckets.
//
// A text whose words were all dropped is hashed whole, because a vector of
// nothing but zeros has no angle: cosine distance against it is not a number,
// and an index would sort that document nowhere rather than last. Empty text is
// the one case with nothing to hash, and it is the case that says nothing.
func (e Embedder) vector(text string) []float32 {
	vector := make([]float32, e.width)
	said := words(text)
	if len(said) == 0 {
		if whole := strings.ToLower(strings.TrimSpace(text)); whole != "" {
			said = []string{whole}
		}
	}
	for _, word := range said {
		vector[bucket(word, e.width)]++
	}
	return vector
}

// words is the text as its words: lowercased runs of letters and digits, the
// short ones dropped. A word said twice is two words here, because how often a
// text says something is what is being counted.
func words(text string) []string {
	found := strings.FieldsFunc(strings.ToLower(text), notWord)
	return slices.DeleteFunc(found, func(word string) bool { return len(word) < shortestWord })
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// bucket is where a word is counted: the word's hash and nothing else, so the
// bucket does not move when the text around the word changes.
func bucket(word string, width int) uint32 {
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(word))
	return digest.Sum32() % uint32(width)
}
