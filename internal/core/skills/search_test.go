package skills_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/skills"
)

// The library the discovery tests ask questions of. The descriptions overlap on
// purpose — three of them are about a CV — so that ranking has to separate them
// rather than find the only card with a word in it.
var library = []skills.Skill{
	{Card: skills.Card{Name: "tailor-cv", Description: "Rewrite a CV to answer one job advert."}, Body: "b"},
	{Card: skills.Card{Name: "screen-cv", Description: "Score a CV against a role and explain the gaps."}, Body: "b"},
	{Card: skills.Card{Name: "format-cv", Description: "Lay a CV out as a single readable page."}, Body: "b"},
	{Card: skills.Card{Name: "draft-email", Description: "Write a short follow-up email to a recruiter."}, Body: "b"},
	{Card: skills.Card{Name: "track-outcome", Description: "Record whether an application was rejected."}, Body: "b"},
}

func newLibrary(t *testing.T) *skills.Registry {
	t.Helper()

	registry, _ := newRegistry(t)
	for _, sk := range library {
		if err := registry.Create(t.Context(), here, sk); err != nil {
			t.Fatalf("Create(%q): %v", sk.Name, err)
		}
	}
	return registry
}

// The acceptance: a task described in the caller's words, not the library's,
// comes back with the skill for it first.
func TestSearchReturnsTheSkillForTheTask(t *testing.T) {
	registry := newLibrary(t)

	for _, tc := range []struct {
		task string
		want string
	}{
		{"rewrite my CV for this advert", "tailor-cv"},
		{"explain the gaps against the role", "screen-cv"},
		{"follow up with the recruiter by email", "draft-email"},
		{"record that the application was rejected", "track-outcome"},
	} {
		matches, err := registry.Search(t.Context(), here, tc.task, 0)
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.task, err)
		}
		if len(matches) == 0 {
			t.Fatalf("Search(%q) found nothing", tc.task)
		}
		if matches[0].Card.Name != tc.want {
			t.Fatalf("Search(%q) = %q, want %q first", tc.task, matches[0].Card.Name, tc.want)
		}
	}
}

// A ranking is only worth having if it puts things in order. "CV" alone reaches
// three skills; the task says which of them.
func TestSearchOrdersTheSkillsThatShareAWord(t *testing.T) {
	registry := newLibrary(t)

	matches, err := registry.Search(t.Context(), here, "lay out my CV on one page", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) < 2 {
		t.Fatalf("Search = %+v, want more than one skill about a CV", matches)
	}
	if matches[0].Card.Name != "format-cv" {
		t.Fatalf("Search = %q first, want format-cv", matches[0].Card.Name)
	}
	if !slices.IsSortedFunc(matches, func(a, b skills.Match) int { return int(b.Score*1e6) - int(a.Score*1e6) }) {
		t.Fatalf("Search = %+v, want scores descending", matches)
	}
}

func TestSearchWithNothingRelevantReturnsNothing(t *testing.T) {
	registry := newLibrary(t)

	matches, err := registry.Search(t.Context(), here, "provision a kubernetes cluster", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("Search = %+v, want nothing rather than everything in score order", matches)
	}
}

func TestSearchLimitsWhatComesBack(t *testing.T) {
	registry := newLibrary(t)

	matches, err := registry.Search(t.Context(), here, "rewrite my CV for this advert", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("Search = %d matches, want 2", len(matches))
	}
	if matches[0].Card.Name != "tailor-cv" {
		t.Fatalf("Search = %q first, want tailor-cv", matches[0].Card.Name)
	}
}

// The registry holds a Matcher and nothing else about ranking, so a matcher
// that answers differently changes discovery with no change here.
func TestSearchAsksTheMatcherAndReportsWhatItSays(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")

	swapped, err := skills.NewRegistry(fsys, root, everything{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	matches, err := swapped.Search(t.Context(), here, "nothing in common", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0].Card.Name != "apply" {
		t.Fatalf("Search = %+v, want what the swapped matcher said", matches)
	}

	failing, err := skills.NewRegistry(fsys, root, refusing{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := failing.Search(t.Context(), here, "anything", 0); !errors.Is(err, errNoOpinion) {
		t.Fatalf("Search = %v, want the matcher's error", err)
	}
}

// everything ranks every card, however little it has to do with the task.
type everything struct{}

func (everything) Rank(_ context.Context, _ string, cards []skills.Card) ([]skills.Match, error) {
	matches := make([]skills.Match, len(cards))
	for i, card := range cards {
		matches[i] = skills.Match{Card: card, Score: 1}
	}
	return matches, nil
}

var errNoOpinion = errors.New("matcher has no opinion")

// refusing is a matcher that fails, which a remote one can.
type refusing struct{}

func (refusing) Rank(context.Context, string, []skills.Card) ([]skills.Match, error) {
	return nil, errNoOpinion
}
