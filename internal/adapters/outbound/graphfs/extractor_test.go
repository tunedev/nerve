package graphfs_test

import (
	"context"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// kind is the one sort of thing the stub extractor names, standing in for
// whatever vocabulary a real endpoint has.
const kind = "mention"

// statements is an extractor that reads a text as one claim per line, spelled
// "from predicate to". It is not a language model and does not pretend to be:
// what the graph's own cases assert is what a graph does with an extraction,
// and a stub that answers exactly what a case wrote is what lets them say so.
//
// The real path is asserted separately, against nounlink.
type statements struct {
	// refuse, when set, is what every extraction answers with.
	refuse error
}

func (s statements) ExtractEntities(_ context.Context, text string) (llm.Extraction, error) {
	if s.refuse != nil {
		return llm.Extraction{}, s.refuse
	}

	read := llm.Extraction{}
	named := map[string]struct{}{}
	for _, line := range strings.Split(text, "\n") {
		words := strings.Fields(line)
		if len(words) != 3 {
			continue
		}
		from := llm.Entity{Kind: kind, Name: words[0]}
		to := llm.Entity{Kind: kind, Name: words[2]}
		for _, entity := range []llm.Entity{from, to} {
			if _, already := named[entity.Name]; !already {
				named[entity.Name] = struct{}{}
				read.Entities = append(read.Entities, entity)
			}
		}
		read.Relations = append(read.Relations, llm.Relation{From: from, Predicate: words[1], To: to})
	}
	return read, nil
}

var _ llm.EntityExtractor = statements{}
