package archive_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/archive"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const sessionRoot = "/sessions"

var here = workspace.Scope{User: "ada", Workspace: "hunt"}

var epoch = time.Date(2026, time.September, 4, 9, 0, 0, 0, time.UTC)

func ticking() func() time.Time {
	step := 0
	return func() time.Time {
		now := epoch.Add(time.Duration(step) * time.Second)
		step++
		return now
	}
}

// summariser answers with whatever a test set, keeping the text it was handed
// so a case can say what the model was actually shown.
type summariser struct {
	summary   string
	err       error
	saw       string
	sentences int
	calls     int
}

func (s *summariser) Summarise(_ context.Context, text string, sentences int) (string, error) {
	s.calls++
	s.saw, s.sentences = text, sentences
	return s.summary, s.err
}

func newArchiver(t *testing.T, summarise llm.Summariser, sentences int) (*archive.Archiver, *session.Store) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(sessionRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", sessionRoot, err)
	}
	sessions, err := session.NewStore(table, sessionRoot, ticking())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	archiver, err := archive.New(sessions, summarise, sentences)
	if err != nil {
		t.Fatalf("archive.New: %v", err)
	}
	return archiver, sessions
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

func turns(messages []session.Message) []string {
	said := make([]string, 0, len(messages))
	for _, m := range messages {
		said = append(said, m.Role+": "+m.Body)
	}
	return said
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The acceptance: the cold half of a conversation is moved aside and a summary
// of it is retained on the session.
func TestAColdTranscriptIsMovedAsideWithASummaryRetained(t *testing.T) {
	summarise := &summariser{summary: "they tracked it east"}
	archiver, sessions := newArchiver(t, summarise, 2)
	mustTalk(t, sessions, "monday",
		[2]string{"user", "where did it go"},
		[2]string{"assistant", "east"},
		[2]string{"user", "and now"},
		[2]string{"assistant", "still east"},
	)

	archived, err := archiver.Archive(t.Context(), here, "monday", 1)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Summary != "they tracked it east" || archived.Archived != 3 {
		t.Fatalf("Archive = %+v, want the summary retained through turn 3", archived)
	}

	got, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !equal(turns(got.Messages), []string{"assistant: still east"}) {
		t.Fatalf("transcript = %v, want only the turn that was kept", turns(got.Messages))
	}
	if got.Session.Summary != "they tracked it east" {
		t.Errorf("stored Summary = %q, want the summariser's answer", got.Session.Summary)
	}

	cold, err := sessions.Archived(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Archived: %v", err)
	}
	want := []string{"user: where did it go", "assistant: east", "user: and now"}
	if !equal(turns(cold), want) {
		t.Fatalf("Archived = %v, want %v", turns(cold), want)
	}

	wantSaw := "user: where did it go\nassistant: east\nuser: and now"
	if summarise.saw != wantSaw {
		t.Errorf("the summariser saw %q, want %q", summarise.saw, wantSaw)
	}
	if summarise.sentences != 2 {
		t.Errorf("the summariser was asked for %d sentences, want 2", summarise.sentences)
	}
}

// TestAShortConversationIsNotArchived: archiving on a schedule must not cost a
// model call per idle session.
func TestAShortConversationIsNotArchived(t *testing.T) {
	summarise := &summariser{summary: "unused"}
	archiver, sessions := newArchiver(t, summarise, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "hello"})

	archived, err := archiver.Archive(t.Context(), here, "monday", 4)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Summary != "" || archived.Archived != 0 {
		t.Errorf("Archive = %+v, want the session untouched", archived)
	}
	if summarise.calls != 0 {
		t.Errorf("the summariser was called %d times, want none", summarise.calls)
	}

	got, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("transcript = %v, want the one turn still in it", turns(got.Messages))
	}
}

// TestASecondArchiveSummarisesWhatTheFirstRetained: the summary a session keeps
// stands for its whole archive, so the second call is shown the first's answer
// rather than replacing it with a summary of the newly cold turns alone.
func TestASecondArchiveSummarisesWhatTheFirstRetained(t *testing.T) {
	summarise := &summariser{summary: "they spoke about the hunt"}
	archiver, sessions := newArchiver(t, summarise, 1)
	mustTalk(t, sessions, "long",
		[2]string{"user", "turn one"},
		[2]string{"assistant", "turn two"},
		[2]string{"user", "turn three"},
	)

	if _, err := archiver.Archive(t.Context(), here, "long", 2); err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	summarise.summary = "they spoke about the hunt, then about the weather"
	if _, err := archiver.Archive(t.Context(), here, "long", 0); err != nil {
		t.Fatalf("second Archive: %v", err)
	}

	wantSaw := "they spoke about the hunt\nassistant: turn two\nuser: turn three"
	if summarise.saw != wantSaw {
		t.Errorf("the second call saw %q, want %q", summarise.saw, wantSaw)
	}

	got, err := sessions.Get(t.Context(), here, "long")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 0 {
		t.Fatalf("transcript = %v, want nothing left in it", turns(got.Messages))
	}
	if got.Session.Summary != "they spoke about the hunt, then about the weather" {
		t.Errorf("Summary = %q, want the second answer", got.Session.Summary)
	}
	cold, err := sessions.Archived(t.Context(), here, "long")
	if err != nil {
		t.Fatalf("Archived: %v", err)
	}
	if len(cold) != 3 {
		t.Fatalf("Archived = %v, want all three turns", turns(cold))
	}
}

// TestASummaryThatRetainsNothingIsRefused: an archive with no summary is a
// conversation whose cold half no reader of the session can reach.
func TestASummaryThatRetainsNothingIsRefused(t *testing.T) {
	archiver, sessions := newArchiver(t, &summariser{summary: ""}, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "hello"}, [2]string{"assistant", "hello"})

	if _, err := archiver.Archive(t.Context(), here, "monday", 1); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Archive with an empty summary = %v, want fs.ErrInvalid", err)
	}
	got, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 2 || got.Session.Archived != 0 {
		t.Fatalf("session = %+v with %v, want the conversation the refusal left alone", got.Session, turns(got.Messages))
	}
}

// TestAFailedSummaryLeavesTheTranscriptAlone: nothing moves until there is a
// summary to move it under.
func TestAFailedSummaryLeavesTheTranscriptAlone(t *testing.T) {
	sunk := errors.New("the endpoint is down")
	archiver, sessions := newArchiver(t, &summariser{err: sunk}, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "hello"}, [2]string{"assistant", "hello"})

	if _, err := archiver.Archive(t.Context(), here, "monday", 1); !errors.Is(err, sunk) {
		t.Fatalf("Archive = %v, want the summariser's failure", err)
	}
	got, err := sessions.Get(t.Context(), here, "monday")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("transcript = %v, want both turns still in it", turns(got.Messages))
	}
	if cold, err := sessions.Archived(t.Context(), here, "monday"); err != nil || len(cold) != 0 {
		t.Fatalf("Archived = %v, %v, want nothing moved aside", turns(cold), err)
	}
}

// TestAnArchiverWithNoSummariserIsRefusedAtTheWiring: a build with no
// summarising endpoint finds out where it is wired, not on the first long
// conversation.
func TestAnArchiverWithNoSummariserIsRefusedAtTheWiring(t *testing.T) {
	table := vfs.NewMountTable()
	if err := table.Mount(sessionRoot, memfs.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	sessions, err := session.NewStore(table, sessionRoot, ticking())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}

	for _, refused := range []struct {
		what      string
		sessions  *session.Store
		summarise llm.Summariser
		sentences int
	}{
		{"no summariser", sessions, nil, 1},
		{"no session store", nil, &summariser{}, 1},
		{"no sentences", sessions, &summariser{}, 0},
	} {
		t.Run(refused.what, func(t *testing.T) {
			if _, err := archive.New(refused.sessions, refused.summarise, refused.sentences); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("New = %v, want fs.ErrInvalid", err)
			}
		})
	}
}

// TestArchivingASessionNobodyHadReportsItMissing: a misspelled name must not
// look like a conversation with nothing in it.
func TestArchivingASessionNobodyHadReportsItMissing(t *testing.T) {
	archiver, _ := newArchiver(t, &summariser{summary: "unused"}, 1)

	if _, err := archiver.Archive(t.Context(), here, "tuesday", 0); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Archive of a session nobody created = %v, want fs.ErrNotExist", err)
	}
}

// TestKeepingFewerThanNoTurnsIsRefused: a negative count is a caller that has
// computed one, and archiving the whole conversation is what keep zero says.
func TestKeepingFewerThanNoTurnsIsRefused(t *testing.T) {
	archiver, sessions := newArchiver(t, &summariser{summary: "unused"}, 1)
	mustTalk(t, sessions, "monday", [2]string{"user", "hello"})

	if _, err := archiver.Archive(t.Context(), here, "monday", -1); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Archive keeping -1 = %v, want fs.ErrInvalid", err)
	}
}
