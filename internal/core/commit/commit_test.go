package commit_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
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

// summariser answers with whatever a test set, keeping the document it was
// handed so a case can say what the endpoint was actually shown.
type summariser struct {
	summary   string
	err       error
	saw       string
	sentences int
	calls     int
}

func (s *summariser) Summarise(_ context.Context, text string, sentences int) (string, error) {
	s.calls++
	s.saw = text
	s.sentences = sentences
	return s.summary, s.err
}

// The policy every test builds a committer under unless it says otherwise: the
// two most recent turns stay as they were said, and three sentences stand for
// everything before them.
var keepTwo = commit.Compression{Keep: 2, Sentences: 3}

func newCommitter(t *testing.T, extract llm.FactExtractor) (*commit.Committer, *session.Store, *memory.Store) {
	t.Helper()
	return build(t, extract, &summariser{summary: "what they talked about"}, keepTwo)
}

func build(t *testing.T, extract llm.FactExtractor, summarise llm.Summariser, policy commit.Compression) (*commit.Committer, *session.Store, *memory.Store) {
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
	committer, err := commit.New(sessions, memories, extract, summarise, policy)
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

	done, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(done.Memories) != 2 {
		t.Fatalf("Commit wrote %+v, want two memories", done.Memories)
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

	done, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(done.Memories) != 0 {
		t.Errorf("Commit wrote %+v, want nothing", done.Memories)
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

// A build whose chain does not serve one of the two capabilities has no
// committer, and neither has one told to keep no conversation hot or to write
// no sentences. The gateway answers nil for a capability nothing serves, and a
// committer built on that nil would accept every session and fail on each one.
func TestACommitterNeedsBothEndpointsAndAUsablePolicy(t *testing.T) {
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
	if _, err := commit.New(sessions, memories, nil, &summariser{}, keepTwo); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no extractor = %v, want fs.ErrInvalid", err)
	}
	if _, err := commit.New(sessions, memories, &extractor{}, nil, keepTwo); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("New with no summariser = %v, want fs.ErrInvalid", err)
	}
	for _, policy := range []commit.Compression{{Keep: 0, Sentences: 3}, {Keep: 2, Sentences: 0}} {
		if _, err := commit.New(sessions, memories, &extractor{}, &summariser{}, policy); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("New under %+v = %v, want fs.ErrInvalid", policy, err)
		}
	}
}

// talk is a conversation of n turns, alternating speakers, each turn saying
// which one it is so a case can name the ones it expects to be cold.
func talk(n int) [][2]string {
	said := make([][2]string, n)
	for i := range said {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		said[i] = [2]string{role, fmt.Sprintf("turn %d", i+1)}
	}
	return said
}

// The acceptance: the cold transcript is reduced to what is worth keeping. Two
// turns stay as they were said and everything before them is a summary — and
// the summariser is handed the cold turns with their speakers, because who said
// a thing is half of what it means.
func TestTheColdTranscriptIsReducedToADigest(t *testing.T) {
	said := &summariser{summary: "they talked about the pumps"}
	committer, sessions, _ := build(t, &extractor{}, said, keepTwo)
	mustTalk(t, sessions, "monday", talk(5)...)

	done, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if done.Digest.Summary != "they talked about the pumps" || done.Digest.Through != 3 {
		t.Fatalf("Commit reduced to %+v, want the summary through message 3", done.Digest)
	}
	if said.sentences != keepTwo.Sentences {
		t.Errorf("the summariser was given %d sentences, want %d", said.sentences, keepTwo.Sentences)
	}
	want := "user: turn 1\n\nassistant: turn 2\n\nuser: turn 3"
	if said.saw != want {
		t.Errorf("the summariser saw %q, want %q", said.saw, want)
	}

	read, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if read.Digest != done.Digest {
		t.Errorf("the session carries %+v, want %+v", read.Digest, done.Digest)
	}
	hot := read.Hot()
	if len(hot) != 2 || hot[0].Body != "turn 4" || hot[1].Body != "turn 5" {
		t.Errorf("Hot = %+v, want the two turns the digest does not cover", hot)
	}
	if len(read.Messages) != 5 {
		t.Errorf("Get = %d messages, want all 5: a commit reduces nothing away", len(read.Messages))
	}
}

// A conversation no longer than the hot half has no cold half, so there is
// nothing to reduce and no endpoint is asked to reduce it.
func TestAShortConversationIsNotCompressed(t *testing.T) {
	said := &summariser{summary: "the pumps"}
	committer, sessions, _ := build(t, &extractor{}, said, keepTwo)
	mustTalk(t, sessions, "monday", talk(2)...)

	done, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if (done.Digest != session.Digest{}) {
		t.Errorf("Commit reduced to %+v, want no digest", done.Digest)
	}
	if said.calls != 0 {
		t.Errorf("the summariser saw %d calls, want 0: there was no cold half", said.calls)
	}
}

// Committing a conversation that has not grown compresses nothing and calls no
// endpoint, and answers with the digest the session already had. Extraction is
// not like that — the same facts are written again, which is E8.5's ticket —
// and the two stages differing is the point of the case.
func TestASecondCommitOverAnUngrownConversationCompressesNothing(t *testing.T) {
	said := &summariser{summary: "the pumps"}
	committer, sessions, _ := build(t, &extractor{}, said, keepTwo)
	mustTalk(t, sessions, "monday", talk(5)...)

	first, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	again, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit again: %v", err)
	}
	if again.Digest != first.Digest {
		t.Errorf("the second commit reduced to %+v, want %+v", again.Digest, first.Digest)
	}
	if said.calls != 1 {
		t.Errorf("the summariser saw %d calls, want 1: the cold half was already covered", said.calls)
	}
}

// The cold half follows the conversation. Turns that were hot become cold as
// more is said, and the digest that covers them is made from the whole cold
// half rather than folded into the one standing: a summary of a summary drifts
// from the conversation and there is no way back to the turns it came from.
func TestCompressionFollowsTheConversationAsItGrows(t *testing.T) {
	said := &summariser{summary: "the pumps"}
	committer, sessions, _ := build(t, &extractor{}, said, keepTwo)
	mustTalk(t, sessions, "monday", talk(5)...)
	if _, err := committer.Commit(t.Context(), here, "monday"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	for _, turn := range talk(7)[5:] {
		if err := sessions.Append(t.Context(), here, "monday", turn[0], turn[1]); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	said.summary = "the pumps and the gate"
	done, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit after more was said: %v", err)
	}
	if done.Digest.Summary != "the pumps and the gate" || done.Digest.Through != 5 {
		t.Fatalf("Commit reduced to %+v, want the summary through message 5", done.Digest)
	}
	if !strings.HasPrefix(said.saw, "user: turn 1\n\n") || !strings.HasSuffix(said.saw, "\n\nuser: turn 5") {
		t.Errorf("the summariser saw %q, want the whole cold half from turn 1 to turn 5", said.saw)
	}
}

// A failed summary writes nothing at all. Both endpoints answer before anything
// is stored, so a workspace whose summariser is down is the workspace it was
// rather than one holding a conversation's facts and no record of the half they
// came from.
func TestAFailedSummaryWritesNothing(t *testing.T) {
	found := &extractor{facts: []llm.Fact{{Kind: "preference", Body: "tabs", Priority: llm.Normal}}}
	down := &summariser{err: fmt.Errorf("endpoint is down: %w", vfs.ErrUnavailable)}
	committer, sessions, memories := build(t, found, down, keepTwo)
	mustTalk(t, sessions, "monday", talk(5)...)

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, vfs.ErrUnavailable) {
		t.Fatalf("Commit over a failing summariser = %v, want the endpoint's failure", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
	read, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if (read.Digest != session.Digest{}) {
		t.Errorf("the session carries %+v, want no digest", read.Digest)
	}
}

// An endpoint that answered no summary did not summarise. Recording it would
// mark the cold half reduced to nothing, so the commit fails and the session
// keeps the digest it had.
func TestASummaryThatSaysNothingIsRefused(t *testing.T) {
	committer, sessions, memories := build(t, &extractor{}, &summariser{}, keepTwo)
	mustTalk(t, sessions, "monday", talk(5)...)

	if _, err := committer.Commit(t.Context(), here, "monday"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Commit over an empty summary = %v, want fs.ErrInvalid", err)
	}
	if stored, err := memories.List(t.Context(), here); err != nil || len(stored) != 0 {
		t.Errorf("List = %+v, %v, want nothing", stored, err)
	}
}

// The cold half is rendered as a document the laptop's own summariser reads
// correctly: each turn its own paragraph, so no sentence comes back that two
// turns were run together into. It is asked over the real endpoint rather than
// a stand-in because the rendering is a claim about what a summariser does with
// a newline, and a stand-in that ignores its input cannot fail that claim.
func TestTheColdHalfReadsAsAConversationToARealSummariser(t *testing.T) {
	committer, sessions, _ := build(t, &extractor{}, termsum.New(), commit.Compression{Keep: 2, Sentences: 2})
	said := [][2]string{
		{"user", "always use tabs"},
		{"assistant", "Noted."},
		{"user", "The water pumps on the east side are failing."},
		{"assistant", "I will check the pressure logs."},
		{"user", "Thanks."},
	}
	mustTalk(t, sessions, "monday", said...)

	done, err := committer.Commit(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if done.Digest.Through != 3 {
		t.Fatalf("Commit reduced through message %d, want 3", done.Digest.Through)
	}

	rendered := map[string]bool{}
	for _, turn := range said[:3] {
		rendered[turn[0]+": "+turn[1]] = true
	}
	lines := strings.Split(done.Digest.Summary, "\n")
	if len(lines) > 2 {
		t.Fatalf("the summary is %d sentences, want at most 2", len(lines))
	}
	for _, line := range lines {
		if !rendered[line] {
			t.Errorf("the summary says %q, which is no turn of the cold half", line)
		}
	}
}
