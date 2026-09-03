package termmatch

import (
	"context"

	"github.com/tunedev/nerve/internal/core/search"
)

// Ranker ranks a query against the entries under a search prefix. It holds
// nothing, for the reason Matcher holds nothing: the weights come from the
// entries it is handed, so a prefix that gains a file ranks differently on the
// next call with no index to rebuild.
type Ranker struct{}

// NewRanker returns the ranker.
func NewRanker() Ranker { return Ranker{} }

// Rank scores each entry by how much of the query's distinctive vocabulary it
// uses, and returns the ones that use any of it, best first.
//
// An entry is its path and its content. Where a thing is kept is part of what
// it is about — a memory is filed under a name someone chose — and a path
// segment every entry shares weighs nothing, so including it costs a prefix of
// uniform names nothing and tells a prefix of meaningful ones something.
func (Ranker) Rank(_ context.Context, query string, entries []search.Entry) ([]search.Hit, error) {
	subjects := make([]subject, len(entries))
	for i, entry := range entries {
		subjects[i] = subject{name: entry.Path, text: entry.Path + " " + entry.Text}
	}

	hits := make([]search.Hit, 0, len(entries))
	for _, ranked := range rank(query, subjects) {
		hits = append(hits, search.Hit{Path: entries[ranked.index].Path, Score: ranked.score})
	}
	return hits, nil
}
