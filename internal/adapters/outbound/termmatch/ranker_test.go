package termmatch_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/search"
)

func hits(t *testing.T, query string, entries ...search.Entry) []search.Hit {
	t.Helper()

	found, err := termmatch.NewRanker().Rank(t.Context(), query, entries)
	if err != nil {
		t.Fatalf("Rank(%q): %v", query, err)
	}
	return found
}

// Where a thing is kept is part of what it is about. Two entries saying the
// same thing are separated by the names someone filed them under, which is the
// only thing the caller gave the ranker to tell them apart.
func TestThePathIsPartOfWhatIsRanked(t *testing.T) {
	found := hits(t, "what are the salary expectations",
		search.Entry{Path: "notice-period.md", Text: "Ada wrote this down in March."},
		search.Entry{Path: "salary-expectations.md", Text: "Ada wrote this down in March."},
	)
	if len(found) == 0 {
		t.Fatalf("Rank found nothing")
	}
	if found[0].Path != "salary-expectations.md" {
		t.Fatalf("Rank = %q first, want salary-expectations.md", found[0].Path)
	}
}

// An entry that shares none of the query's vocabulary is not relevant, and
// saying so by putting it last is how everything ends up in a prompt.
func TestNothingSharedIsNoHit(t *testing.T) {
	found := hits(t, "kubernetes and terraform",
		search.Entry{Path: "salary.md", Text: "Ada will not take a role under ninety thousand pounds."},
	)
	if len(found) != 0 {
		t.Fatalf("Rank = %+v, want nothing", found)
	}
}
