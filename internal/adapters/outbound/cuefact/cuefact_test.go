package cuefact_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/cuefact"
	"github.com/tunedev/nerve/internal/core/llm"
)

func extract(t *testing.T, turns ...llm.Turn) []llm.Fact {
	t.Helper()
	facts, err := cuefact.New(cuefact.Role).Extract(t.Context(), turns)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return facts
}

func said(body string) llm.Turn { return llm.Turn{Role: "user", Body: body} }

// A sentence carrying a cue comes back typed by the cue and ranked by how
// firmly it was said, in the order the person said it.
func TestACuedSentenceIsATypedRankedFact(t *testing.T) {
	facts := extract(t, said("My name is Ada. I prefer tabs. Never force push."))

	want := []llm.Fact{
		{Kind: "identity", Body: "My name is Ada.", Priority: llm.High},
		{Kind: "preference", Body: "I prefer tabs.", Priority: llm.Normal},
		{Kind: "instruction", Body: "Never force push.", Priority: llm.High},
	}
	if len(facts) != len(want) {
		t.Fatalf("Extract = %+v, want %+v", facts, want)
	}
	for i, fact := range facts {
		if fact != want[i] {
			t.Errorf("fact %d = %+v, want %+v", i, fact, want[i])
		}
	}
}

// Only what the person said is extracted. A model's own suggestion stored as
// something the user asked for is the worst memory this can write.
func TestOnlyThePersonsTurnsAreRead(t *testing.T) {
	facts := extract(t,
		said("I prefer tabs."),
		llm.Turn{Role: "assistant", Body: "I prefer spaces. Never use tabs."},
	)
	if len(facts) != 1 || facts[0].Body != "I prefer tabs." {
		t.Errorf("Extract = %+v, want only what the user said", facts)
	}
}

// A hedged sentence is still worth keeping and is worth dropping first, so it
// comes back one rung down. Priority is not a second spelling of the kind.
func TestAHedgedSentenceRanksLower(t *testing.T) {
	facts := extract(t, said("I think we should always run the tests."))

	if len(facts) != 1 {
		t.Fatalf("Extract = %+v, want one fact", facts)
	}
	if facts[0].Kind != "instruction" || facts[0].Priority != llm.Normal {
		t.Errorf("Extract = %+v, want an instruction one rung below High", facts[0])
	}
}

// A hedge cannot rank a fact off the bottom of the scale. A hedged aside is
// still something the person said, and a priority the store cannot order is a
// fact it would refuse.
func TestAHedgeCannotRankAFactOffTheScale(t *testing.T) {
	facts := extract(t, said("Maybe I'm a Go programmer, I'm not sure."))

	if len(facts) != 1 {
		t.Fatalf("Extract = %+v, want one fact", facts)
	}
	if facts[0].Priority != llm.Low {
		t.Errorf("priority = %v, want Low", facts[0].Priority)
	}
}

// A sentence several cues catch is answered once. The same sentence stored
// twice under two kinds is one fact a caller has to reconcile against itself.
func TestASentenceIsAnsweredOnce(t *testing.T) {
	facts := extract(t, said("Never change my name is what I mean."))

	if len(facts) != 1 || facts[0].Kind != "instruction" {
		t.Errorf("Extract = %+v, want one instruction", facts)
	}
}

// A transcript with no cue in it extracts to nothing, which is an answer. Most
// of a conversation is not facts, and an extractor that always found one would
// fill a workspace with sentences nobody wants back.
func TestAConversationWithNoCueExtractsNothing(t *testing.T) {
	if facts := extract(t, said("Hello. How does the mount table work?")); len(facts) != 0 {
		t.Errorf("Extract = %+v, want nothing", facts)
	}
	if facts := extract(t); len(facts) != 0 {
		t.Errorf("Extract over no turns = %+v, want nothing", facts)
	}
}

// The role is the caller's vocabulary, so a build whose sessions call the
// person something else says so once, in config.
func TestTheRoleIsConfigured(t *testing.T) {
	facts, err := cuefact.New("human").Extract(t.Context(), []llm.Turn{
		said("I prefer tabs."),
		{Role: "human", Body: "I prefer spaces."},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(facts) != 1 || facts[0].Body != "I prefer spaces." {
		t.Errorf("Extract = %+v, want only the configured role's turn", facts)
	}
}

// The endpoint serves extraction and nothing else. A chain asked for a summary
// passes over it.
func TestTheProviderServesExtractionAlone(t *testing.T) {
	provider, err := cuefact.Factory(nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if provider.AsFactExtractor() == nil {
		t.Error("AsFactExtractor = nil, want the extractor")
	}
	if verb := provider.AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil", verb)
	}
}

// A configured role reaches the extractor the factory builds.
func TestTheFactoryReadsTheRole(t *testing.T) {
	provider, err := cuefact.Factory(map[string]string{cuefact.RoleSetting: "human"})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	facts, err := provider.AsFactExtractor().Extract(t.Context(), []llm.Turn{{Role: "human", Body: "I prefer tabs."}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(facts) != 1 {
		t.Errorf("Extract = %+v, want the configured role's one fact", facts)
	}
}
