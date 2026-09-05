package commit_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/archive"
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

// summariser answers with whatever a test set, keeping the text it was handed
// so a case can say what the model was actually shown.
type summariser struct {
	summary string
	err     error
	saw     string
	calls   int
}

func (s *summariser) Summarise(_ context.Context, text string, _ int) (string, error) {
	s.calls++
	s.saw = text
	return s.summary, s.err
}

func newCommitter(t *testing.T, extract llm.FactExtractor, summarise llm.Summariser, keep int) (*commit.Committer, *session.Store, *memory.Store) {
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
	archiver, err := archive.New(sessions, summarise, 1)
	if err != nil {
		t.Fatalf("archive.New: %v", err)
	}
	committer, err := commit.New(sessions, memories, extract, archiver, keep)
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
	committer, sessions, memories := newCommitter(t, found, &summariser{summary: "they talked"}, 2)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs. Never force push."}, [2]string{"assistant", "Noted."})

	result, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(result.Memories) != 2 {
		t.Fatalf("Commit wrote %+v, want two memories", result.Memories)
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

// The other acceptance: what a committed conversation leaves behind is its most
// recent turns and one summary standing for everything before them. Nothing is
// thrown away — the cold turns keep the sequences they were said under and are
// read back from the archive.
func TestACommitReducesTheColdTranscriptToASummaryTheSessionKeeps(t *testing.T) {
	said := &summariser{summary: "they argued about indentation"}
	committer, sessions, _ := newCommitter(t, &extractor{}, said, 1)
	mustTalk(t, sessions, "monday",
		[2]string{"user", "tabs"},
		[2]string{"assistant", "spaces"},
		[2]string{"user", "tabs, finally"},
		[2]string{"assistant", "as you like"},
	)

	result, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Session.Summary != said.summary || result.Session.Archived != 3 {
		t.Errorf("Commit answered with %+v, want the summary through sequence 3", result.Session)
	}

	transcript, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(transcript.Messages) != 1 || transcript.Messages[0].Body != "as you like" {
		t.Errorf("the session now reads %+v, want the last turn alone", transcript.Messages)
	}
	if transcript.Session.Summary != said.summary {
		t.Errorf("the session retains %q, want %q", transcript.Session.Summary, said.summary)
	}

	cold, err := sessions.Archived(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Archived: %v", err)
	}
	if len(cold) != 3 || cold[0].Seq != 1 || cold[2].Seq != 3 {
		t.Errorf("the archive holds %+v, want sequences 1 to 3", cold)
	}
}

// The extractor is handed the conversation as it was said: every turn, with its
// speaker, in order. A preference the user stated is a fact about the user, and
// the same sentence in the assistant's own output is a fact about nothing, so
// the port cannot be given the bodies alone.
func TestTheExtractorIsHandedTheConversationInOrder(t *testing.T) {
	found := &extractor{}
	committer, sessions, _ := newCommitter(t, found, &summariser{summary: "they talked"}, 2)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"}, [2]string{"assistant", "I prefer spaces"})

	if _, err := committer.Commit(t.Context(), here, "monday"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	want := []llm.Turn{{Role: "user", Body: "I prefer tabs"}, {Role: "assistant", Body: "I prefer spaces"}}
	if len(found.saw) != 2 || found.saw[0] != want[0] || found.saw[1] != want[1] {
		t.Errorf("the extractor saw %+v, want %+v", found.saw, want)
	}
}

// Extraction reads the whole conversation, including the half this commit is
// about to move aside. Archiving first would narrow the transcript under the
// stage that has to read all of it, and the facts in the cold half would never
// be written by this run or by any later one.
func TestTheExtractorSeesTheColdHalfBeforeItGoesCold(t *testing.T) {
	found := &extractor{}
	committer, sessions, _ := newCommitter(t, found, &summariser{summary: "they talked"}, 1)
	mustTalk(t, sessions, "monday",
		[2]string{"user", "I prefer tabs"},
		[2]string{"assistant", "noted"},
		[2]string{"user", "and never force push"},
	)

	if _, err := committer.Commit(t.Context(), here, "monday"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(found.saw) != 3 {
		t.Errorf("the extractor saw %+v, want all three turns", found.saw)
	}
}

// A conversation short enough to keep is not summarised at all: no endpoint is
// called, nothing moves, and the session carries no summary. A caller
// committing every session it opens would otherwise pay a model call for a
// conversation with two turns in it.
func TestASessionShortEnoughToKeepIsNotArchived(t *testing.T) {
	said := &summariser{summary: "they talked"}
	committer, sessions, memories := newCommitter(t, &extractor{facts: []llm.Fact{{Kind: "preference", Body: "tabs", Priority: llm.Low}}}, said, 4)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"})

	result, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if said.calls != 0 {
		t.Errorf("the summariser saw %d calls, want 0: the conversation is shorter than the policy keeps", said.calls)
	}
	if result.Session.Summary != "" || result.Session.Archived != 0 {
		t.Errorf("Commit answered with %+v, want a session nothing was archived from", result.Session)
	}
	if len(result.Memories) != 1 {
		t.Errorf("Commit wrote %+v, want the one memory", result.Memories)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 1 {
		t.Errorf("List = %+v, %v, want the one memory", stored, err)
	}
}

// A conversation that has not grown is already reduced. The second commit finds
// the cold half archived and the hot half within the policy, calls no
// summariser, and answers with the summary the session already carried.
func TestASecondCommitOverAConversationThatHasNotGrownSummarisesNothing(t *testing.T) {
	said := &summariser{summary: "they argued about indentation"}
	committer, sessions, _ := newCommitter(t, &extractor{}, said, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "tabs"}, [2]string{"assistant", "spaces"}, [2]string{"user", "tabs"})

	for range 2 {
		if _, err := committer.Commit(t.Context(), here, "monday"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	if said.calls != 1 {
		t.Errorf("the summariser saw %d calls, want 1: the second commit had nothing new to reduce", said.calls)
	}
	transcript, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if transcript.Session.Archived != 2 || transcript.Session.Summary != said.summary {
		t.Errorf("the session stands at %+v, want the first commit's archival", transcript.Session)
	}
}

// A conversation with nothing worth remembering commits nothing, and that is an
// answer rather than a failure.
func TestACommitWithNothingWorthKeepingWritesNothing(t *testing.T) {
	committer, sessions, memories := newCommitter(t, &extractor{}, &summariser{summary: "they talked"}, 2)
	mustTalk(t, sessions, "monday", [2]string{"user", "hello"})

	result, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(result.Memories) != 0 {
		t.Errorf("Commit wrote %+v, want nothing", result.Memories)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
}

// A session nobody created fails the commit. A misspelled name that quietly
// wrote nothing looks exactly like a conversation with nothing in it.
func TestCommittingASessionNobodyHadFails(t *testing.T) {
	found, said := &extractor{}, &summariser{summary: "they talked"}
	committer, _, _ := newCommitter(t, found, said, 2)

	if _, err := committer.Commit(t.Context(), here, "never-happened"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Commit of an absent session = %v, want fs.ErrNotExist", err)
	}
	if found.calls != 0 || said.calls != 0 {
		t.Errorf("the endpoints saw %d extractions and %d summaries, want none: there was no transcript to read", found.calls, said.calls)
	}
}

// A failed extraction writes no memories and moves nothing aside. Half a
// conversation's facts, with no record of which half, is worse than none of
// them — and a transcript reduced on behalf of an extraction that did not
// happen is worse still.
func TestAFailedExtractionWritesNothing(t *testing.T) {
	said := &summariser{summary: "they talked"}
	committer, sessions, memories := newCommitter(t, &extractor{err: fmt.Errorf("endpoint is down: %w", vfs.ErrUnavailable)}, said, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"}, [2]string{"assistant", "noted"})

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, vfs.ErrUnavailable) {
		t.Fatalf("Commit over a failing endpoint = %v, want the endpoint's failure", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
	if said.calls != 0 {
		t.Errorf("the summariser saw %d calls, want 0", said.calls)
	}
	if transcript, err := sessions.Get(t.Context(), here, "monday"); err != nil || len(transcript.Messages) != 2 {
		t.Errorf("the transcript is now %+v, %v, want both turns still in it", transcript.Messages, err)
	}
}

// A failed summarisation leaves every message where it was said, so running the
// commit again reduces the same cold half. The memories this run wrote stand:
// the reduction is the destructive stage and it runs last, because a commit
// interrupted the other way round would extract from a transcript it had
// already narrowed and lose what the cold half said.
func TestAFailedSummarisationLeavesTheColdHalfWhereItWasSaid(t *testing.T) {
	said := &summariser{err: fmt.Errorf("endpoint is down: %w", vfs.ErrUnavailable)}
	committer, sessions, memories := newCommitter(t, &extractor{facts: []llm.Fact{{Kind: "preference", Body: "tabs", Priority: llm.Low}}}, said, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"}, [2]string{"assistant", "noted"})

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, vfs.ErrUnavailable) {
		t.Fatalf("Commit over a failing summariser = %v, want the endpoint's failure", err)
	}
	transcript, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(transcript.Messages) != 2 || transcript.Session.Summary != "" {
		t.Errorf("the session is now %+v with %+v, want both turns and no summary", transcript.Session, transcript.Messages)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 1 {
		t.Errorf("List = %+v, %v, want the memory the extraction wrote", stored, err)
	}
}

// An endpoint answering a rank this build cannot order is refused rather than
// filed under the nearest one, none of its batch is stored, and nothing is
// moved aside on behalf of a commit that did not happen.
func TestAPriorityTheEndpointInventedIsRefused(t *testing.T) {
	said := &summariser{summary: "they talked"}
	committer, sessions, memories := newCommitter(t, &extractor{facts: []llm.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		{Kind: "instruction", Body: "never force push", Priority: llm.Priority(9)},
	}}, said, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "I prefer tabs"}, [2]string{"assistant", "noted"})

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Commit of a fact off the scale = %v, want fs.ErrInvalid", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
	if transcript, err := sessions.Get(t.Context(), here, "monday"); err != nil || len(transcript.Messages) != 2 {
		t.Errorf("the transcript is now %+v, %v, want both turns still in it", transcript.Messages, err)
	}
}

// A build whose chain extracts nothing has no committer. The gateway answers
// nil for a capability nothing serves, and a committer built on that nil would
// accept every session and fail on each one.
func TestACommitterNeedsAnExtractor(t *testing.T) {
	sessions, memories, archiver := parts(t)
	if _, err := commit.New(sessions, memories, nil, archiver, 2); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no extractor = %v, want fs.ErrInvalid", err)
	}
}

// And a build with nothing to reduce a cold transcript has no committer either,
// for the same reason: an archiver is what a build without a summarising
// endpoint does not have, and a committer holding nil would accept every
// session and fail on each one past the extraction.
func TestACommitterNeedsAnArchiver(t *testing.T) {
	sessions, memories, _ := parts(t)
	if _, err := commit.New(sessions, memories, &extractor{}, nil, 2); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no archiver = %v, want fs.ErrInvalid", err)
	}
}

// Keeping nothing hot is refused rather than read as "reduce the whole
// conversation": it is the setting that hands a caller a paragraph where the
// turn it is answering should be.
func TestACommitterKeepingNoTurnsHotIsRefused(t *testing.T) {
	sessions, memories, archiver := parts(t)
	if _, err := commit.New(sessions, memories, &extractor{}, archiver, 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New keeping no turns = %v, want fs.ErrInvalid", err)
	}
}

// parts builds what a committer is made of, for the cases about refusing one of
// them.
func parts(t *testing.T) (*session.Store, *memory.Store, *archive.Archiver) {
	t.Helper()

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
	archiver, err := archive.New(sessions, &summariser{summary: "they talked"}, 1)
	if err != nil {
		t.Fatalf("archive.New: %v", err)
	}
	return sessions, memories, archiver
}
