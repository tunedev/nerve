package lifecycle_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/consolidate"
	"github.com/tunedev/nerve/internal/core/lifecycle"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/memories"

var here = workspace.Scope{User: "ada", Workspace: "hunt"}

var epoch = time.Date(2026, time.September, 6, 9, 0, 0, 0, time.UTC)

func at(step int) time.Time { return epoch.Add(time.Duration(step) * time.Second) }

func ticking() func() time.Time {
	step := 0
	return func() time.Time {
		now := at(step)
		step++
		return now
	}
}

var (
	dublin = memory.Fact{Kind: "identity", Body: "lives in Dublin", Priority: memory.Low}
	berlin = memory.Fact{Kind: "identity", Body: "lives in Berlin", Priority: memory.Low}
	tabs   = memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal}
	never  = memory.Fact{Kind: "instruction", Body: "never force push", Priority: memory.High}
)

// merging answers with one narrative over the first two facts it is shown,
// which is the crudest answer that is still a merge.
type merging struct{ calls int }

func (m *merging) Consolidate(_ context.Context, facts []llm.Fact) ([]llm.Narrative, error) {
	m.calls++
	if len(facts) < 2 {
		return nil, nil
	}
	return []llm.Narrative{{Facts: []int{0, 1}, Body: facts[0].Body + ", then " + facts[1].Body}}, nil
}

func newRoutes(t *testing.T, merge llm.FactConsolidator) (*lifecycle.Routes, *memory.Store) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	memories, err := memory.NewStore(table, root, ticking())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	consolidator, err := consolidate.New(memories, merge)
	if err != nil {
		t.Fatalf("consolidate.New: %v", err)
	}
	routes, err := lifecycle.NewRoutes(memories, consolidator)
	if err != nil {
		t.Fatalf("NewRoutes: %v", err)
	}
	return routes, memories
}

func mustAdd(t *testing.T, memories *memory.Store, source string, facts ...memory.Fact) []memory.Memory {
	t.Helper()
	written, err := memories.Add(t.Context(), here, source, facts)
	if err != nil {
		t.Fatalf("Add(%q): %v", source, err)
	}
	return written
}

func bodies(memories []memory.Memory) []string {
	said := make([]string, len(memories))
	for i, m := range memories {
		said[i] = m.Fact.Body
	}
	return said
}

func seqsOf(memories []memory.Memory) []int {
	seqs := make([]int, len(memories))
	for i, m := range memories {
		seqs[i] = m.Seq
	}
	return seqs
}

// The acceptance: what the archive route sets aside is out of what the
// workspace believes, and the restore route puts it back.
func TestArchivedMemoryIsRecoverableThroughTheRoutes(t *testing.T) {
	routes, memories := newRoutes(t, &merging{})
	mustAdd(t, memories, "monday", dublin, never)

	if err := routes.Archive(t.Context(), here, []int{1}); err != nil {
		t.Fatalf("Archive(1): %v", err)
	}
	current, err := memories.Current(t.Context(), here)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if want := []string{never.Body}; !slices.Equal(bodies(current), want) {
		t.Errorf("Current = %v, want %v", bodies(current), want)
	}

	if err := routes.Restore(t.Context(), here, []int{1}); err != nil {
		t.Fatalf("Restore(1): %v", err)
	}
	current, err = memories.Current(t.Context(), here)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if want := []string{dublin.Body, never.Body}; !slices.Equal(bodies(current), want) {
		t.Errorf("Current after Restore = %v, want %v", bodies(current), want)
	}
}

// The consolidate route is the merging stage reached through the same surface,
// and what it writes is a memory the log holds like any other.
func TestTheConsolidateRouteMergesWhatTheWorkspaceBelieves(t *testing.T) {
	merge := &merging{}
	routes, memories := newRoutes(t, merge)
	mustAdd(t, memories, "monday", dublin, berlin)

	written, err := routes.Consolidate(t.Context(), here, "tuesday")
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if merge.calls != 1 {
		t.Errorf("the endpoint was called %d times, want once", merge.calls)
	}
	if len(written) != 1 || !slices.Equal(written[0].Supersedes, []int{1, 2}) {
		t.Fatalf("Consolidate wrote %+v, want one narrative replacing 1 and 2", written)
	}
	current, err := memories.Current(t.Context(), here)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if len(current) != 1 || current[0].Seq != 3 {
		t.Errorf("Current = %+v, want the narrative alone", current)
	}
}

// The temporal graph query answers with how a belief came to be what it is: the
// memory named, what it replaced and what replaced that, each carrying the
// interval it was believed over.
func TestLineageWalksTheSupersessionGraphInBothDirections(t *testing.T) {
	routes, memories := newRoutes(t, &merging{})
	mustAdd(t, memories, "monday", dublin, tabs)
	if _, err := memories.Supersede(t.Context(), here, "tuesday", []int{1}, []memory.Fact{berlin}); err != nil {
		t.Fatalf("Supersede: %v", err)
	}

	lineage, err := routes.Lineage(t.Context(), here, 3)
	if err != nil {
		t.Fatalf("Lineage(3): %v", err)
	}
	if want := []int{1, 3}; !slices.Equal(seqsOf(lineage), want) {
		t.Fatalf("Lineage(3) = %v, want %v", seqsOf(lineage), want)
	}
	if lineage[0].Until != at(2) {
		t.Errorf("the replaced memory ran until %v, want %v", lineage[0].Until, at(2))
	}
	if !lineage[1].Until.IsZero() {
		t.Errorf("the replacing memory ran until %v, want nothing to have replaced it", lineage[1].Until)
	}

	back, err := routes.Lineage(t.Context(), here, 1)
	if err != nil {
		t.Fatalf("Lineage(1): %v", err)
	}
	if want := []int{1, 3}; !slices.Equal(seqsOf(back), want) {
		t.Errorf("Lineage(1) = %v, want %v", seqsOf(back), want)
	}
}

// The graph does not stop at the archive. A memory set aside is still how the
// workspace came to believe what replaced it, and a lineage with a hole in it
// would say a belief came from nowhere.
func TestLineageReachesIntoTheArchive(t *testing.T) {
	routes, memories := newRoutes(t, &merging{})
	mustAdd(t, memories, "monday", dublin)
	if _, err := memories.Supersede(t.Context(), here, "tuesday", []int{1}, []memory.Fact{berlin}); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if err := routes.Archive(t.Context(), here, []int{1}); err != nil {
		t.Fatalf("Archive(1): %v", err)
	}

	lineage, err := routes.Lineage(t.Context(), here, 2)
	if err != nil {
		t.Fatalf("Lineage(2): %v", err)
	}
	if want := []int{1, 2}; !slices.Equal(seqsOf(lineage), want) {
		t.Errorf("Lineage(2) = %v, want %v", seqsOf(lineage), want)
	}
}

// A memory the workspace never wrote has no lineage, and the caller finds out
// rather than reading an empty answer as a belief that came from nowhere.
func TestTheLineageOfAMemoryNobodyWroteIsRefused(t *testing.T) {
	routes, memories := newRoutes(t, &merging{})
	mustAdd(t, memories, "monday", dublin)

	if _, err := routes.Lineage(t.Context(), here, 9); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Lineage(9) = %v, want fs.ErrNotExist", err)
	}
	if _, err := routes.Lineage(t.Context(), here, 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Lineage(0) = %v, want fs.ErrInvalid", err)
	}
}

// A memory nothing has replaced and that replaced nothing is its own lineage.
func TestAMemoryThatStandsAloneIsItsOwnLineage(t *testing.T) {
	routes, memories := newRoutes(t, &merging{})
	mustAdd(t, memories, "monday", dublin)

	lineage, err := routes.Lineage(t.Context(), here, 1)
	if err != nil {
		t.Fatalf("Lineage(1): %v", err)
	}
	if want := []int{1}; !slices.Equal(seqsOf(lineage), want) {
		t.Errorf("Lineage(1) = %v, want %v", seqsOf(lineage), want)
	}
}

// Routes missing either half are refused where they are built. A build that
// looks wired and fails on the first call it is asked to serve is worse than
// one that will not come up.
func TestIncompleteRoutesAreRefused(t *testing.T) {
	table := vfs.NewMountTable()
	if err := table.Mount(root, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	memories, err := memory.NewStore(table, root, ticking())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	consolidator, err := consolidate.New(memories, &merging{})
	if err != nil {
		t.Fatalf("consolidate.New: %v", err)
	}

	if _, err := lifecycle.NewRoutes(nil, consolidator); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewRoutes(no store) = %v, want fs.ErrInvalid", err)
	}
	if _, err := lifecycle.NewRoutes(memories, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewRoutes(no consolidator) = %v, want fs.ErrInvalid", err)
	}
}
