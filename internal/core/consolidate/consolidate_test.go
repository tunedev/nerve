package consolidate_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/consolidate"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const memoryRoot = "/memories"

var here = workspace.Scope{User: "ada", Workspace: "hunt"}

var epoch = time.Date(2026, time.September, 6, 9, 0, 0, 0, time.UTC)

func ticking() func() time.Time {
	step := 0
	return func() time.Time {
		now := epoch.Add(time.Duration(step) * time.Second)
		step++
		return now
	}
}

// merging answers with whatever a test set, keeping the facts it was handed so
// a case can say what the endpoint was actually shown.
type merging struct {
	narratives []llm.Narrative
	err        error
	saw        []llm.Fact
	calls      int
}

func (m *merging) Consolidate(_ context.Context, facts []llm.Fact) ([]llm.Narrative, error) {
	m.calls++
	m.saw = facts
	return m.narratives, m.err
}

// byKind merges everything filed under one category, which is the crudest
// answer a consolidator can give that is still an answer. It is here for the
// cases about what a second run finds.
type byKind struct{ calls int }

func (b *byKind) Consolidate(_ context.Context, facts []llm.Fact) ([]llm.Narrative, error) {
	b.calls++
	var narratives []llm.Narrative
	for _, kind := range kinds(facts) {
		var merged llm.Narrative
		for i, fact := range facts {
			if fact.Kind == kind {
				merged.Facts = append(merged.Facts, i)
				merged.Body += fact.Body + ". "
			}
		}
		if len(merged.Facts) > 1 {
			narratives = append(narratives, merged)
		}
	}
	return narratives, nil
}

func kinds(facts []llm.Fact) []string {
	var found []string
	for _, fact := range facts {
		if !slices.Contains(found, fact.Kind) {
			found = append(found, fact.Kind)
		}
	}
	return found
}

func newConsolidator(t *testing.T, merge llm.FactConsolidator) (*consolidate.Consolidator, *memory.Store) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(memoryRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", memoryRoot, err)
	}
	memories, err := memory.NewStore(table, memoryRoot, ticking())
	if err != nil {
		t.Fatalf("memory.NewStore: %v", err)
	}
	consolidator, err := consolidate.New(memories, merge)
	if err != nil {
		t.Fatalf("consolidate.New: %v", err)
	}
	return consolidator, memories
}

func mustBelieve(t *testing.T, memories *memory.Store, source string, facts ...memory.Fact) {
	t.Helper()
	if _, err := memories.Add(t.Context(), here, source, facts); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func bodies(memories []memory.Memory) []string {
	said := make([]string, len(memories))
	for i, m := range memories {
		said[i] = m.Fact.Body
	}
	return said
}

// The acceptance: related facts merge. What the workspace believes afterwards
// is the narrative, and the facts it merged are still in the log with the
// interval they were believed over.
func TestRelatedFactsMergeIntoOneNarrative(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{{Facts: []int{0, 1}, Body: "prefers tabs, and tabs in Go"}}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal},
	)

	written, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(written) != 1 || written[0].Fact.Body != "prefers tabs, and tabs in Go" {
		t.Fatalf("Consolidate wrote %+v, want the one narrative", written)
	}
	if !slices.Equal(written[0].Supersedes, []int{1, 2}) {
		t.Errorf("the narrative supersedes %v, want the two facts it merged", written[0].Supersedes)
	}

	current, err := memories.Current(t.Context(), here)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if len(current) != 1 || current[0].Seq != written[0].Seq {
		t.Errorf("the workspace believes %v, want the narrative alone", bodies(current))
	}
	stored, err := memories.List(t.Context(), here)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("List = %v, want the two facts and the narrative", bodies(stored))
	}
	if stored[0].Until.IsZero() || stored[1].Until.IsZero() {
		t.Errorf("the merged facts are believed still: %+v", stored[:2])
	}
}

// The narrative keeps the highest priority of what it merged. A model that
// ranked its own sentence could quietly demote what a user said never to do,
// so the rank is derived here from the memories rather than answered there.
func TestTheNarrativeKeepsTheHighestPriorityOfWhatItMerged(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{{Facts: []int{0, 1}, Body: "never force push, especially on main"}}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "instruction", Body: "never force push", Priority: memory.High},
		memory.Fact{Kind: "instruction", Body: "never force push on main", Priority: memory.Low},
	)

	written, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(written) != 1 || written[0].Fact.Priority != memory.High {
		t.Errorf("the narrative is ranked %+v, want high: it merged a high instruction", written)
	}
	if written[0].Fact.Kind != "instruction" {
		t.Errorf("the narrative is filed under %q, want the category it merged", written[0].Fact.Kind)
	}
}

// The endpoint is shown what the workspace believes and nothing else. A memory
// something already replaced is a belief the workspace gave up, and merging it
// back in would make the log say it never changed its mind.
func TestTheEndpointIsShownOnlyWhatTheWorkspaceBelieves(t *testing.T) {
	found := &merging{}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday", memory.Fact{Kind: "preference", Body: "prefers spaces", Priority: memory.Normal})
	if _, err := memories.Supersede(t.Context(), here, "monday", []int{1}, []memory.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
	}); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	mustBelieve(t, memories, "monday", memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal})

	if _, err := consolidator.Consolidate(t.Context(), here, "tuesday"); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	want := []llm.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
	}
	if !slices.Equal(found.saw, want) {
		t.Errorf("the endpoint saw %+v, want %+v", found.saw, want)
	}
}

// A workspace with one memory has nothing to merge, and no endpoint is called
// for it. A caller consolidating on every commit would otherwise pay a model
// call for a workspace that has heard one thing.
func TestAWorkspaceWithNothingToMergeCallsNoEndpoint(t *testing.T) {
	found := &merging{}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday", memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal})

	written, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(written) != 0 || found.calls != 0 {
		t.Errorf("Consolidate wrote %+v after %d calls, want nothing and no call", written, found.calls)
	}
}

// A narrative naming one fact is not a merge. Writing it would supersede a
// memory with a rewording of itself, and the run after it would do so again.
func TestANarrativeNamingOneFactIsNotWritten(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{{Facts: []int{0}, Body: "prefers tabs, really"}}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "instruction", Body: "never force push", Priority: memory.High},
	)

	written, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Consolidate wrote %+v, want nothing: a narrative of one fact merges nothing", written)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 2 {
		t.Errorf("List = %v, %v, want the two facts unchanged", bodies(stored), err)
	}
}

// A narrative merging two categories is not written. The category is what a
// fact was filed under, and a workspace asked for its instructions must not
// answer with a habit nobody told it to keep.
func TestANarrativeMixingCategoriesIsNotWritten(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{{Facts: []int{0, 1}, Body: "tabs, and never force push"}}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "instruction", Body: "never force push", Priority: memory.High},
	)

	written, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Consolidate wrote %+v, want nothing: a preference and an instruction are not one claim", written)
	}
}

// A narrative merging a fact that answers a question is not written. A later
// answer to that question replaces the memory answering it, and a narrative
// with the answer folded into it would keep saying the old one beside the new.
func TestANarrativeMergingAnAnswerIsNotWritten(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{{Facts: []int{0, 1}, Body: "I live in Dublin; I work in Dublin"}}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "identity", Subject: "home", Body: "I live in Dublin.", Priority: memory.High},
		memory.Fact{Kind: "identity", Body: "I am in Dublin most weeks.", Priority: memory.Normal},
	)

	written, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Consolidate wrote %+v, want nothing: an answer stands on its own", written)
	}
}

// The endpoint is shown the question each fact answers, so it can leave
// answers alone rather than propose a merge the stage will decline.
func TestTheEndpointIsShownTheQuestionAFactAnswers(t *testing.T) {
	found := &merging{}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "identity", Subject: "home", Body: "I live in Dublin.", Priority: memory.High},
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
	)

	if _, err := consolidator.Consolidate(t.Context(), here, "tuesday"); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(found.saw) != 2 || found.saw[0].Subject != "home" || found.saw[1].Subject != "" {
		t.Errorf("the endpoint was shown %+v, want the home subject on the first fact alone", found.saw)
	}
}

// A narrative naming a position that was not in the batch is a broken answer
// rather than a judgement, and none of the batch is written on the strength of
// it: the caller cannot tell which of the other narratives that endpoint meant.
func TestANarrativeNamingAFactThatWasNotInTheBatchIsRefused(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{
		{Facts: []int{0, 1}, Body: "prefers tabs everywhere"},
		{Facts: []int{0, 7}, Body: "and something nobody said"},
	}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal},
	)

	if _, err := consolidator.Consolidate(t.Context(), here, "tuesday"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Consolidate over an answer naming nothing = %v, want fs.ErrInvalid", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 2 {
		t.Errorf("List = %v, %v, want the two facts unchanged", bodies(stored), err)
	}
}

// One memory merged into two narratives is refused for the same reason: the
// claim would stand twice, in two sentences, and the interval it was believed
// over would close against whichever narrative was written first.
func TestAMemoryMergedIntoTwoNarrativesIsRefused(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{
		{Facts: []int{0, 1}, Body: "prefers tabs everywhere"},
		{Facts: []int{1, 2}, Body: "prefers tabs in Go and in Rust"},
	}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Rust", Priority: memory.Normal},
	)

	if _, err := consolidator.Consolidate(t.Context(), here, "tuesday"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Consolidate over an answer merging one memory twice = %v, want fs.ErrInvalid", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 3 {
		t.Errorf("List = %v, %v, want the three facts unchanged", bodies(stored), err)
	}
}

// A narrative with nothing in it is a broken answer too: it would supersede two
// memories with a memory that says nothing.
func TestANarrativeWithNoBodyIsRefused(t *testing.T) {
	found := &merging{narratives: []llm.Narrative{{Facts: []int{0, 1}}}}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal},
	)

	if _, err := consolidator.Consolidate(t.Context(), here, "tuesday"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Consolidate over a narrative saying nothing = %v, want fs.ErrInvalid", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 2 {
		t.Errorf("List = %v, %v, want the two facts unchanged", bodies(stored), err)
	}
}

// Consolidating a workspace that has heard nothing new merges nothing. What the
// second run is shown is the narrative the first one wrote, which merges with
// nothing, so a caller consolidating on every commit does not rewrite the log
// on every commit.
func TestASecondConsolidationOfTheSameBeliefsMergesNothing(t *testing.T) {
	found := &byKind{}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal},
	)

	first, err := consolidator.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	again, err := consolidator.Consolidate(t.Context(), here, "wednesday")
	if err != nil {
		t.Fatalf("Consolidate again: %v", err)
	}
	if len(first) != 1 || len(again) != 0 {
		t.Errorf("the runs wrote %v then %v, want one narrative and then nothing", bodies(first), bodies(again))
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 3 {
		t.Errorf("List = %v, %v, want the two facts and the one narrative", bodies(stored), err)
	}
}

// A failing endpoint writes nothing. Half a workspace's beliefs merged, with no
// record of which half, is worse than none of them.
func TestAFailingEndpointMergesNothing(t *testing.T) {
	found := &merging{err: fmt.Errorf("endpoint is down: %w", stability.ErrUnavailable)}
	consolidator, memories := newConsolidator(t, found)
	mustBelieve(t, memories, "monday",
		memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		memory.Fact{Kind: "preference", Body: "prefers tabs in Go", Priority: memory.Normal},
	)

	if _, err := consolidator.Consolidate(t.Context(), here, "tuesday"); !errors.Is(err, stability.ErrUnavailable) {
		t.Fatalf("Consolidate over a failing endpoint = %v, want the endpoint's failure", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 2 {
		t.Errorf("List = %v, %v, want the two facts unchanged", bodies(stored), err)
	}
}

// A build whose chain merges nothing has no consolidator. The gateway answers
// nil for a capability nothing serves, and a consolidator built on that nil
// would accept every workspace and fail on each one.
func TestAConsolidatorNeedsAnEndpoint(t *testing.T) {
	table := vfs.NewMountTable()
	if err := table.Mount(memoryRoot, memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	memories, err := memory.NewStore(table, memoryRoot, ticking())
	if err != nil {
		t.Fatalf("memory.NewStore: %v", err)
	}
	if _, err := consolidate.New(memories, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no endpoint = %v, want fs.ErrInvalid", err)
	}
	if _, err := consolidate.New(nil, &merging{}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no memory store = %v, want fs.ErrInvalid", err)
	}
}
