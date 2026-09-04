package commit_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/commit"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const (
	sessionRoot = "/sessions"
	memoryRoot  = "/memories"
)

var here = workspace.Scope{User: "ada", Workspace: "hunt"}

var epoch = time.Date(2026, time.September, 3, 9, 0, 0, 0, time.UTC)

func ticking() func() time.Time {
	step := 0
	return func() time.Time {
		now := epoch.Add(time.Duration(step) * time.Second)
		step++
		return now
	}
}

// extractor answers with whatever a test set, keeping the turns it was handed
// so a case can say what the model was actually shown.
type extractor struct {
	facts []llm.Fact
	err   error
	saw   []llm.Turn
	calls int
}

func (e *extractor) Extract(_ context.Context, turns []llm.Turn) ([]llm.Fact, error) {
	e.calls++
	e.saw = turns
	return e.facts, e.err
}

func newCommitter(t *testing.T, extract llm.FactExtractor) (*commit.Committer, *session.Store, *memory.Store) {
	t.Helper()

	table := vfs.NewMountTable()
	for _, root := range []string{sessionRoot, memoryRoot} {
		if err := table.Mount(root, memfs.New()); err != nil {
			t.Fatalf("Mount(%q): %v", root, err)
		}
	}
	sessions, err := session.NewStore(table, sessionRoot, ticking())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	memories, err := memory.NewStore(table, memoryRoot, ticking())
	if err != nil {
		t.Fatalf("memory.NewStore: %v", err)
	}
	committer, err := commit.New(sessions, memories, extract)
	if err != nil {
		t.Fatalf("commit.New: %v", err)
	}
	return committer, sessions, memories
}

func mustTalk(t *testing.T, sessions *session.Store, name string, said ...[2]string) {
	t.Helper()
	if err := sessions.Create(t.Context(), here, name, nil); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	for _, turn := range said {
		if err := sessions.Append(t.Context(), here, name, turn[0], turn[1]); err != nil {
			t.Fatalf("Append(%q, %q): %v", turn[0], turn[1], err)
		}
	}
}

// The acceptance: a transcript becomes typed memories, ranked, and each naming
// the session it came out of.
func TestATranscriptBecomesTypedMemories(t *testing.T) {
	found := &extractor{facts: []llm.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		{Kind: "instruction", Body: "never force push", Priority: llm.High},
	}}
	committer, sessions, memories := newCommitter(t, found)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs. Never force push."}, [2]string{"assistant", "Noted."})

	written, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("Commit wrote %+v, want two memories", written)
	}

	stored, err := memories.List(t.Context(), here)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("List = %d memories, want 2", len(stored))
	}
	want := []memory.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal},
		{Kind: "instruction", Body: "never force push", Priority: memory.High},
	}
	for i, m := range stored {
		if m.Fact != want[i] {
			t.Errorf("memory %d = %+v, want %+v", m.Seq, m.Fact, want[i])
		}
		if m.Source != "monday" {
			t.Errorf("memory %d came from %q, want monday", m.Seq, m.Source)
		}
	}
}

// The extractor is handed the conversation as it was said: every turn, with its
// speaker, in order. A preference the user stated is a fact about the user, and
// the same sentence in the assistant's own output is a fact about nothing, so
// the port cannot be given the bodies alone.
func TestTheExtractorIsHandedTheConversationInOrder(t *testing.T) {
	found := &extractor{}
	committer, sessions, _ := newCommitter(t, found)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"}, [2]string{"assistant", "I prefer spaces"})

	if _, err := committer.Commit(t.Context(), here, "monday"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	want := []llm.Turn{{Role: "user", Body: "I prefer tabs"}, {Role: "assistant", Body: "I prefer spaces"}}
	if len(found.saw) != 2 || found.saw[0] != want[0] || found.saw[1] != want[1] {
		t.Errorf("the extractor saw %+v, want %+v", found.saw, want)
	}
}

// A conversation with nothing worth remembering commits nothing, and that is an
// answer rather than a failure.
func TestACommitWithNothingWorthKeepingWritesNothing(t *testing.T) {
	committer, sessions, memories := newCommitter(t, &extractor{})
	mustTalk(t, sessions, "monday", [2]string{"user", "hello"})

	written, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Commit wrote %+v, want nothing", written)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
}

// A commit reads the transcript and never writes it. What is derived from a
// session is derived from the session that was there, so a commit run twice
// leaves the same conversation behind it.
func TestACommitLeavesTheTranscriptAsItWas(t *testing.T) {
	committer, sessions, _ := newCommitter(t, &extractor{facts: []llm.Fact{{Kind: "preference", Body: "tabs", Priority: llm.Low}}})
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"})

	before, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for range 2 {
		if _, err := committer.Commit(t.Context(), here, "monday"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	after, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(after.Messages) != len(before.Messages) || after.Messages[0] != before.Messages[0] {
		t.Errorf("the transcript is now %+v, want %+v", after.Messages, before.Messages)
	}
}

// A session nobody created fails the commit. A misspelled name that quietly
// wrote nothing looks exactly like a conversation with nothing in it.
func TestCommittingASessionNobodyHadFails(t *testing.T) {
	found := &extractor{}
	committer, _, _ := newCommitter(t, found)

	if _, err := committer.Commit(t.Context(), here, "never-happened"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Commit of an absent session = %v, want fs.ErrNotExist", err)
	}
	if found.calls != 0 {
		t.Errorf("the extractor saw %d calls, want 0: there was no transcript to read", found.calls)
	}
}

// A failed extraction writes no memories. Half a conversation's facts, with no
// record of which half, is worse than none of them.
func TestAFailedExtractionWritesNoMemories(t *testing.T) {
	committer, sessions, memories := newCommitter(t, &extractor{err: fmt.Errorf("endpoint is down: %w", vfs.ErrUnavailable)})
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"})

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, vfs.ErrUnavailable) {
		t.Fatalf("Commit over a failing endpoint = %v, want the endpoint's failure", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
}

// An endpoint answering a rank this build cannot order is refused rather than
// filed under the nearest one, and none of its batch is stored.
func TestAPriorityTheEndpointInventedIsRefused(t *testing.T) {
	committer, sessions, memories := newCommitter(t, &extractor{facts: []llm.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		{Kind: "instruction", Body: "never force push", Priority: llm.Priority(9)},
	}})
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"})

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Commit of a fact off the scale = %v, want fs.ErrInvalid", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
}

// A build whose chain extracts nothing has no committer. The gateway answers
// nil for a capability nothing serves, and a committer built on that nil would
// accept every session and fail on each one.
func TestACommitterNeedsAnExtractor(t *testing.T) {
	table := vfs.NewMountTable()
	if err := table.Mount(sessionRoot, memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	sessions, err := session.NewStore(table, sessionRoot, ticking())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	memories, err := memory.NewStore(table, sessionRoot, ticking())
	if err != nil {
		t.Fatalf("memory.NewStore: %v", err)
	}
	if _, err := commit.New(sessions, memories, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no extractor = %v, want fs.ErrInvalid", err)
	}
}
