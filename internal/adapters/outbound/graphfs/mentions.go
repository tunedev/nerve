package graphfs

import (
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// mentionHeading names the columns a mentions answer carries. The key comes
// first because that is what a reader takes from this answer: a name into the
// texts, which is the same name the corpus of an index is keyed by.
const mentionHeading = "key\tclaims\n"

// mention is one text that named an entity and how much it said about it.
type mention struct {
	key    string
	claims int
}

// mentions answers with the texts that name the entity called name, the ones
// that say most about it first.
//
// A text counts as naming an entity when the extractor put that name in its
// entities or at either end of one of its claims. Both are checked because a
// text that names things and relates none of them is the ordinary case, and a
// ranking that only saw relations would answer nothing for most of a corpus.
//
// The order is claims first, then the key. A text stating three things about an
// entity is a better answer than one that merely mentions it, and the key after
// it is what makes two runs over one graph answer identically.
//
// Ranking, not scoring. A caller fusing this with another ranking reads the
// order and nothing else: the claim count is here to be read by a person
// debugging an answer, and it is not comparable with anything outside this
// graph.
//
// A name no text names answers with the heading and no rows, for the reason an
// expansion of an unknown entity does: the graph was asked and holds nothing.
func (f *FS) mentions(name string) []byte {
	found := f.mentionsOf(name)
	slices.SortFunc(found, func(a, b mention) int {
		if a.claims != b.claims {
			return b.claims - a.claims
		}
		return strings.Compare(a.key, b.key)
	})
	if len(found) > f.edges {
		found = found[:f.edges]
	}

	report := strings.Builder{}
	report.WriteString(mentionHeading)
	for _, one := range found {
		report.WriteString(one.key)
		report.WriteByte('\t')
		report.WriteString(strconv.Itoa(one.claims))
		report.WriteByte('\n')
	}
	return []byte(report.String())
}

// mentionsOf gathers the texts naming an entity called name, counting the
// distinct claims each states about it.
//
// It walks every text for the reason an expansion does: the texts are the
// record, and an index beside them is a second thing to keep true across every
// write, rename and removal.
func (f *FS) mentionsOf(name string) []mention {
	var found []mention
	f.read(func(documents map[string]document) {
		for key, held := range documents {
			claims := claimsIn(held.Read, name)
			if claims < 0 {
				continue
			}
			found = append(found, mention{key: key, claims: claims})
		}
	})
	return found
}

// claimsIn is how many distinct claims one text states about an entity called
// name, or -1 when the text does not name it at all. The two are different
// answers: a text that names an entity and relates it to nothing states no
// claims about it and is still a text about it.
func claimsIn(read llm.Extraction, name string) int {
	claims := 0
	for _, stated := range distinct(read.Relations) {
		if stated.From.Name == name || stated.To.Name == name {
			claims++
		}
	}
	if claims > 0 {
		return claims
	}
	if slices.ContainsFunc(read.Entities, func(one llm.Entity) bool { return one.Name == name }) {
		return 0
	}
	return -1
}
