package commit_test

import (
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/cuefact"
	"github.com/tunedev/nerve/internal/adapters/outbound/termfold"
	"github.com/tunedev/nerve/internal/core/memory"
)

func believedNow(t *testing.T, memories *memory.Store) []string {
	t.Helper()
	current, err := memories.Current(t.Context(), here)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	return said(current)
}

func said(memories []memory.Memory) []string {
	bodies := make([]string, len(memories))
	for i, m := range memories {
		bodies[i] = m.Fact.Body
	}
	return bodies
}

// The acceptance, through the laptop's own extractor: a conversation that
// contradicts what the workspace believes leaves one memory believed, and the
// one it contradicted is still what the workspace believed before.
func TestAContradictedMemoryIsResolvedNotDuplicated(t *testing.T) {
	committer, sessions, memories := newCommitter(t, cuefact.New(cuefact.Role), &summariser{summary: "they talked"}, 2)
	mustTalk(t, sessions, "monday", [2]string{"user", "My name is Ada."})
	mustTalk(t, sessions, "tuesday", [2]string{"user", "My name is Bea."})

	first, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit(monday): %v", err)
	}
	second, err := committer.Commit(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Commit(tuesday): %v", err)
	}
	if len(second.Memories) != 1 || !slices.Equal(second.Memories[0].Supersedes, []int{first.Memories[0].Seq}) {
		t.Fatalf("the second commit wrote %+v, want Bea replacing Ada", second.Memories)
	}

	if want := []string{"My name is Bea."}; !slices.Equal(believedNow(t, memories), want) {
		t.Errorf("Current = %v, want %v", believedNow(t, memories), want)
	}
	then, err := memories.AsOf(t.Context(), here, first.Memories[0].At)
	if err != nil {
		t.Fatalf("AsOf: %v", err)
	}
	if want := []string{"My name is Ada."}; !slices.Equal(said(then), want) {
		t.Errorf("AsOf(monday) = %v, want %v", said(then), want)
	}
}

// Two facts of one kind that answer no single question are not a
// contradiction. Liking coffee does not stop anyone liking tea.
func TestTwoLikesAreNotAContradiction(t *testing.T) {
	committer, sessions, memories := newCommitter(t, cuefact.New(cuefact.Role), &summariser{summary: "they talked"}, 2)
	mustTalk(t, sessions, "monday", [2]string{"user", "I like tea."})
	mustTalk(t, sessions, "tuesday", [2]string{"user", "I like coffee."})

	for _, name := range []string{"monday", "tuesday"} {
		if _, err := committer.Commit(t.Context(), here, name); err != nil {
			t.Fatalf("Commit(%s): %v", name, err)
		}
	}
	if want := []string{"I like tea.", "I like coffee."}; !slices.Equal(believedNow(t, memories), want) {
		t.Errorf("Current = %v, want %v", believedNow(t, memories), want)
	}
}

// Through the whole laptop chain: two answers alike enough to merge are left
// standing apart, so moving house replaces where the user lived and leaves where
// they work alone.
func TestAnAnswerIsNotMergedAwayFromTheQuestionItAnswers(t *testing.T) {
	merge, err := termfold.New(0.4)
	if err != nil {
		t.Fatalf("termfold.New: %v", err)
	}
	committer, sessions, memories, _ := newMergingCommitter(t, cuefact.New(cuefact.Role), merge, &summariser{summary: "they talked"}, 2)
	mustTalk(t, sessions, "monday", [2]string{"user", "I live in Dublin. I work in Dublin."})
	mustTalk(t, sessions, "tuesday", [2]string{"user", "I live in Berlin."})

	for _, name := range []string{"monday", "tuesday"} {
		if _, err := committer.Commit(t.Context(), here, name); err != nil {
			t.Fatalf("Commit(%s): %v", name, err)
		}
	}
	if want := []string{"I work in Dublin.", "I live in Berlin."}; !slices.Equal(believedNow(t, memories), want) {
		t.Errorf("Current = %v, want %v", believedNow(t, memories), want)
	}
}
