package termfold_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termfold"
	"github.com/tunedev/nerve/internal/core/llm"
)

func merger(t *testing.T, share float64) termfold.Consolidator {
	t.Helper()

	consolidator, err := termfold.New(share)
	if err != nil {
		t.Fatalf("New(%v): %v", share, err)
	}
	return consolidator
}

func merged(t *testing.T, consolidator termfold.Consolidator, facts ...llm.Fact) []llm.Narrative {
	t.Helper()

	narratives, err := consolidator.Consolidate(t.Context(), facts)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	return narratives
}

// The acceptance: a claim and the same claim narrowed are one narrative, and
// the narrative says both of them in the order they were written.
func TestAClaimAndTheSameClaimNarrowedMerge(t *testing.T) {
	narratives := merged(t, merger(t, termfold.DefaultShare),
		llm.Fact{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
	)

	if len(narratives) != 1 {
		t.Fatalf("Consolidate = %+v, want one narrative", narratives)
	}
	if !slices.Equal(narratives[0].Facts, []int{0, 1}) {
		t.Errorf("the narrative merges %v, want both facts", narratives[0].Facts)
	}
	if narratives[0].Body != "prefers tabs; prefers tabs in Go" {
		t.Errorf("the narrative says %q, want both claims", narratives[0].Body)
	}
}

// Two claims that share only the word their category is about are two claims.
// A consolidator that merged them would answer a question about indentation
// with what the user thinks of dark mode.
func TestTwoUnrelatedPreferencesDoNotMerge(t *testing.T) {
	narratives := merged(t, merger(t, termfold.DefaultShare),
		llm.Fact{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "prefers dark mode in the editor", Priority: llm.Normal},
	)

	if len(narratives) != 0 {
		t.Errorf("Consolidate = %+v, want nothing merged", narratives)
	}
}

// Facts filed under different categories never merge, however alike they read.
// "use tabs" as a preference and "use tabs" as an instruction are a habit and
// an order.
func TestFactsInDifferentCategoriesNeverMerge(t *testing.T) {
	narratives := merged(t, merger(t, termfold.DefaultShare),
		llm.Fact{Kind: "preference", Body: "always use tabs", Priority: llm.Normal},
		llm.Fact{Kind: "instruction", Body: "always use tabs", Priority: llm.High},
	)

	if len(narratives) != 0 {
		t.Errorf("Consolidate = %+v, want nothing merged: a habit and an order are not one claim", narratives)
	}
}

// Relatedness carries. A claim narrowed twice is one claim, and answering with
// two overlapping narratives would leave the caller to work out that they are
// about the same thing.
func TestAClaimNarrowedTwiceIsOneNarrative(t *testing.T) {
	narratives := merged(t, merger(t, termfold.DefaultShare),
		llm.Fact{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "prefers tabs in Go tests", Priority: llm.Normal},
	)

	if len(narratives) != 1 || !slices.Equal(narratives[0].Facts, []int{0, 1, 2}) {
		t.Fatalf("Consolidate = %+v, want the three facts in one narrative", narratives)
	}
}

// A narrative the stage would decline is never proposed: every group has more
// than one fact in it, so a workspace of unrelated beliefs merges nothing at
// all rather than being handed back to itself one narrative at a time.
func TestAFactRelatedToNothingIsNotANarrative(t *testing.T) {
	narratives := merged(t, merger(t, termfold.DefaultShare),
		llm.Fact{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "drinks coffee black", Priority: llm.Low},
	)

	if len(narratives) != 1 || !slices.Equal(narratives[0].Facts, []int{0, 1}) {
		t.Errorf("Consolidate = %+v, want the two related facts alone", narratives)
	}
}

// The narrative a run writes merges with nothing on the next run, which is what
// keeps a caller consolidating on every commit from rewriting the log on every
// commit. The narrative shares every word of what it merged, so the case is not
// obvious: it holds because the facts it merged are no longer believed and are
// never shown again.
func TestTheNarrativeOfARunMergesWithNothingOnTheNext(t *testing.T) {
	consolidator := merger(t, termfold.DefaultShare)
	first := merged(t, consolidator,
		llm.Fact{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
	)
	if len(first) != 1 {
		t.Fatalf("Consolidate = %+v, want one narrative", first)
	}

	again := merged(t, consolidator, llm.Fact{Kind: "preference", Body: first[0].Body, Priority: llm.Normal})
	if len(again) != 0 {
		t.Errorf("Consolidate over what the first run wrote = %+v, want nothing", again)
	}
}

// A workspace believing nothing, or one thing, merges nothing.
func TestNothingToMergeIsNoNarratives(t *testing.T) {
	consolidator := merger(t, termfold.DefaultShare)

	if narratives := merged(t, consolidator); len(narratives) != 0 {
		t.Errorf("Consolidate(nothing) = %+v, want nothing", narratives)
	}
	if narratives := merged(t, consolidator, llm.Fact{Kind: "preference", Body: "prefers tabs"}); len(narratives) != 0 {
		t.Errorf("Consolidate of one fact = %+v, want nothing", narratives)
	}
}

// A threshold that merges everything, or nothing, is refused where the endpoint
// is built rather than found out on the first workspace it reads.
func TestAThresholdOffTheScaleIsRefused(t *testing.T) {
	for _, share := range []float64{0, -1, 1.5} {
		if _, err := termfold.New(share); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("New(%v) = %v, want fs.ErrInvalid", share, err)
		}
	}
	if _, err := termfold.New(1); err != nil {
		t.Errorf("New(1) = %v, want the endpoint that merges only identical vocabularies", err)
	}
}

// A paraphrase does not merge, and cannot. "likes tabs" shares one word in three
// with "prefers tabs", which is what "prefers dark mode" shares with it, so no
// threshold this endpoint could take keeps one and drops the other. It is the
// line where a model earns its place, asserted here so that the floor is stated
// rather than assumed.
func TestAParaphraseIsTwoClaimsHere(t *testing.T) {
	narratives := merged(t, merger(t, termfold.DefaultShare),
		llm.Fact{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		llm.Fact{Kind: "preference", Body: "likes tabs", Priority: llm.Normal},
	)

	if len(narratives) != 0 {
		t.Errorf("Consolidate = %+v, want nothing: this endpoint reads words, not meaning", narratives)
	}
}
