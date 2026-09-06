// Package nounlink reads what a document names, and what it says about two of
// those things, out of the shape of its sentences.
//
// It is the laptop's provider: no model, no network, nothing to start. It
// reaches a caller the way every model endpoint does, through the gateway's
// chain, so a build that puts a hosted model in front of it changes a line of
// config, and the graph holding the port cannot tell the two apart.
//
// Be plain about what this is. It reads capitals, not meaning. An entity is a
// run of capitalised words, so it finds "Postgres" and misses "the database"
// beside it, and it will happily name a product nobody would call an entity. A
// relation is one of a short list of phrases standing between two of those
// runs, so "Nerve uses Postgres" is an edge and the same claim made across two
// sentences is nothing at all.
//
// It types every entity Mention, because a capital says a word names something
// and says nothing about what. Deciding that "Postgres" is a database and
// "Furiosa" is a person is what a model is for, and a kind invented here would
// be a guess a graph then had to trust.
package nounlink

import (
	"context"
	"strings"
	"unicode"

	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/core/llm"
)

// Mention is the kind every entity here has: something the text named.
const Mention = "mention"

// starters are the words a sentence opens with that are capitalised because a
// sentence opened, and not because they name anything. A run beginning the
// sentence loses its first word to this list, and a run that was only that word
// is no mention at all.
//
// This is the whole of what stops "The pipeline" being an entity called "The",
// and it is why an entity that only ever opens a sentence is missed. A stopword
// list is the price of not having a model; the alternative was a rule that
// dropped every sentence's first word, which loses the commonest place a
// document names its own subject.
var starters = map[string]bool{
	"a": true, "after": true, "all": true, "although": true, "an": true, "and": true,
	"any": true, "as": true, "at": true, "because": true, "before": true, "both": true,
	"but": true, "by": true, "during": true, "each": true, "every": true, "for": true,
	"from": true, "he": true, "her": true, "here": true, "his": true, "how": true,
	"i": true, "if": true, "in": true, "it": true, "its": true, "most": true,
	"my": true, "no": true, "not": true, "on": true, "once": true, "one": true,
	"or": true, "our": true, "she": true, "since": true, "so": true, "some": true,
	"that": true, "the": true, "their": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "to": true, "until": true, "we": true,
	"what": true, "when": true, "which": true, "while": true, "who": true, "why": true,
	"with": true, "you": true, "your": true,
}

// articles are dropped from the words standing between two mentions, so "uses a
// Postgres" asserts what "uses Postgres" asserts. Without this every predicate
// would need a second spelling with an article in it.
var articles = map[string]bool{"a": true, "an": true, "the": true}

// predicates are the phrases that relate the mention before them to the mention
// after them, and what each one asserts. Adding a relation is a line here.
//
// The phrase has to be the whole of what stands between the two mentions, which
// is what keeps a relation to a claim the sentence actually made: matching a cue
// anywhere in between would relate two names either side of a subordinate
// clause that said nothing about them.
var predicates = map[string]string{
	"is":         "is-a",
	"is part of": "part-of",
	"part of":    "part-of",
	"uses":       "uses",
	"used by":    "used-by",
	"depends on": "depends-on",
	"runs on":    "runs-on",
	"produces":   "produces",
	"replaces":   "replaces",
	"supersedes": "supersedes",
	"owns":       "owns",
	"wrote":      "wrote",
	"written by": "written-by",
	"works at":   "works-at",
	"works for":  "works-at",
	"lives in":   "lives-in",
	"reports to": "reports-to",
}

// Extractor reads entities out of text by capital. It holds nothing: the rules
// are the package's, so there is no index and nothing to invalidate.
type Extractor struct{}

// New returns the extractor.
func New() Extractor { return Extractor{} }

// ExtractEntities returns what the text names and what it says about pairs of
// those things, each in the order the text first said it.
//
// A name said twice is one entity and a claim made twice is one relation: an
// extraction is what a document says, not a count of how often it says it.
//
// A relation is only ever read inside one sentence. Two mentions either side of
// a full stop are two things the document happened to name in a row, and an
// edge between them would be an assertion nobody made.
func (Extractor) ExtractEntities(_ context.Context, text string) (llm.Extraction, error) {
	var found llm.Extraction
	entities, relations := seen{}, seen{}
	for _, sentence := range termsum.Sentences(text) {
		said := tokens(sentence)
		mentions := mentionsIn(said)
		for _, m := range mentions {
			entity := llm.Entity{Kind: Mention, Name: m.name}
			if entities.first(entity.Name) {
				found.Entities = append(found.Entities, entity)
			}
		}
		for _, relation := range relationsIn(said, mentions) {
			if relations.first(relation.From.Name + "\x00" + relation.Predicate + "\x00" + relation.To.Name) {
				found.Relations = append(found.Relations, relation)
			}
		}
	}
	return found, nil
}

// seen keeps what has already been answered, so the order of an extraction is
// the order the document said things and nothing comes back twice.
type seen map[string]bool

// first reports whether key is new, and remembers it.
func (s seen) first(key string) bool {
	if s[key] {
		return false
	}
	s[key] = true
	return true
}

// token is one word of a sentence: what it says with the punctuation around it
// stripped, whether it was capitalised, and whether something ended after it.
//
// The ending matters as much as the capital. "Nerve, Postgres" and "Nerve
// Postgres" are the same two capitalised words in a row, and only the comma
// says they are two names rather than one two-word name.
type token struct {
	word  string
	caps  bool
	ended bool
}

// tokens is the sentence as its words. A word is its letters and digits: the
// quotes, brackets and commas around it belong to the sentence rather than to
// the name, and a possessive is the name with something said about it.
func tokens(sentence string) []token {
	var found []token
	for _, field := range strings.Fields(sentence) {
		word := strings.TrimFunc(field, notWord)
		if word == "" {
			continue
		}
		trimmed := strings.TrimSuffix(word, "'s")
		found = append(found, token{
			word:  trimmed,
			caps:  unicode.IsUpper([]rune(trimmed)[0]),
			ended: !strings.HasSuffix(field, word),
		})
	}
	return found
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// mention is one name a sentence has in it, and where it stands, so the words
// between two of them can be read.
type mention struct {
	name        string
	first, last int
}

// mentionsIn is the names one sentence has in it, in the order it says them: a
// run of capitalised words, ended by the next lowercase word or by the
// punctuation that separates two names.
func mentionsIn(said []token) []mention {
	var found []mention
	for at := 0; at < len(said); {
		if !said[at].caps {
			at++
			continue
		}

		start := at
		for at < len(said) && said[at].caps {
			at++
			if said[at-1].ended {
				break
			}
		}

		run := said[start:at]
		if start == 0 && starters[strings.ToLower(run[0].word)] {
			run, start = run[1:], start+1
		}
		if len(run) == 0 {
			continue
		}
		found = append(found, mention{name: nameOf(run), first: start, last: at - 1})
	}
	return found
}

// nameOf is what a run of capitalised words is called: the words as the
// sentence wrote them.
func nameOf(run []token) string {
	words := make([]string, len(run))
	for i, t := range run {
		words[i] = t.word
	}
	return strings.Join(words, " ")
}

// relationsIn is what a sentence asserted between the names next to each other
// in it.
//
// Only neighbouring mentions are read. "Nerve uses Postgres and RabbitMQ"
// relates the first pair and not the second, because what stands between
// Postgres and RabbitMQ is "and" and nothing here knows that the verb before
// them reaches across it.
func relationsIn(said []token, mentions []mention) []llm.Relation {
	var found []llm.Relation
	for i := 0; i+1 < len(mentions); i++ {
		predicate, asserted := predicates[phrase(said[mentions[i].last+1:mentions[i+1].first])]
		if !asserted {
			continue
		}
		found = append(found, llm.Relation{
			From:      llm.Entity{Kind: Mention, Name: mentions[i].name},
			Predicate: predicate,
			To:        llm.Entity{Kind: Mention, Name: mentions[i+1].name},
		})
	}
	return found
}

// phrase is the words between two mentions as a predicate is written: lowercase
// and without its articles.
func phrase(between []token) string {
	words := make([]string, 0, len(between))
	for _, t := range between {
		word := strings.ToLower(t.word)
		if !articles[word] {
			words = append(words, word)
		}
	}
	return strings.Join(words, " ")
}
