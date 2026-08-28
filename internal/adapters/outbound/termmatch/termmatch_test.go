package termmatch_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/library"
)

func rank(t *testing.T, task string, cards ...library.Card) []library.Match {
	t.Helper()

	matches, err := termmatch.New().Rank(t.Context(), task, cards)
	if err != nil {
		t.Fatalf("Rank(%q): %v", task, err)
	}
	return matches
}

func names(matches []library.Match) []string {
	found := make([]string, len(matches))
	for i, match := range matches {
		found[i] = match.Card.Name
	}
	return found
}

// The word every card uses separates nothing; the word one card uses decides.
func TestTheRarerWordDecidesTheOrder(t *testing.T) {
	matches := rank(t, "rewrite a CV",
		library.Card{Name: "read", Description: "Read a CV."},
		library.Card{Name: "rewrite", Description: "Rewrite a CV."},
		library.Card{Name: "score", Description: "Score a CV."},
	)
	if len(matches) != 3 {
		t.Fatalf("Rank = %v, want every card that shares a word", names(matches))
	}
	if matches[0].Card.Name != "rewrite" {
		t.Fatalf("Rank = %v, want rewrite first", names(matches))
	}
	if matches[1].Score >= matches[0].Score {
		t.Fatalf("Rank scores = %v, want the shared word to score below the rare one", matches)
	}
}

// A card covering the whole task's vocabulary covers all of its weight.
func TestCoveringTheWholeTaskScoresOne(t *testing.T) {
	matches := rank(t, "rewrite a CV", library.Card{Name: "rewrite-cv", Description: "Rewrite a CV."})
	if len(matches) != 1 {
		t.Fatalf("Rank = %v, want the one card", names(matches))
	}
	if matches[0].Score != 1 {
		t.Fatalf("Rank score = %v, want 1", matches[0].Score)
	}
}

// A library of one skill is the case where every word is both universal and
// unique. A weight that reached zero there would answer every task with
// nothing.
func TestALibraryOfOneStillAnswers(t *testing.T) {
	matches := rank(t, "score this CV", library.Card{Name: "screen", Description: "Score a CV against a role."})
	if len(matches) != 1 {
		t.Fatalf("Rank = %v, want the one card", names(matches))
	}
	if matches[0].Score <= 0 {
		t.Fatalf("Rank score = %v, want above zero", matches[0].Score)
	}
}

func TestNothingSharedIsNoMatch(t *testing.T) {
	matches := rank(t, "provision a kubernetes cluster",
		library.Card{Name: "rewrite", Description: "Rewrite a CV."},
	)
	if len(matches) != 0 {
		t.Fatalf("Rank = %v, want nothing", names(matches))
	}
}

func TestAnEmptyTaskOrLibraryIsNoMatch(t *testing.T) {
	if matches := rank(t, "", library.Card{Name: "rewrite", Description: "Rewrite a CV."}); len(matches) != 0 {
		t.Fatalf("Rank(\"\") = %v, want nothing", names(matches))
	}
	if matches := rank(t, "rewrite a CV"); len(matches) != 0 {
		t.Fatalf("Rank over no cards = %v, want nothing", names(matches))
	}
}

// Two cards covering the same words come back in the same order every run, not
// in map order.
func TestATieBreaksOnName(t *testing.T) {
	for range 20 {
		matches := rank(t, "rewrite a CV",
			library.Card{Name: "second", Description: "Rewrite a CV."},
			library.Card{Name: "first", Description: "Rewrite a CV."},
		)
		if len(matches) != 2 || matches[0].Card.Name != "first" {
			t.Fatalf("Rank = %v, want first then second", names(matches))
		}
	}
}

// Case and punctuation are how a task is written, not what it is about.
func TestPunctuationAndCaseDoNotChangeTheAnswer(t *testing.T) {
	card := library.Card{Name: "rewrite-cv", Description: "Rewrite a CV."}
	plain := rank(t, "rewrite cv", card)
	shouted := rank(t, "REWRITE, the C.V.!", card)

	if len(plain) != 1 || len(shouted) != 1 || plain[0].Score != shouted[0].Score {
		t.Fatalf("Rank = %v and %v, want the same answer", plain, shouted)
	}
}
