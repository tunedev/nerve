package session_test

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/session"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/sessions"

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}
)

// epoch is where the fake clock starts, and each stamp is one second later, so
// a case can say which stamp it expects without spelling out a time.
var epoch = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

func at(step int) time.Time { return epoch.Add(time.Duration(step) * time.Second) }

// ticking returns a clock that answers one second later on every call, which is
// what makes a stamp say which write produced it.
func ticking() func() time.Time {
	step := 0
	return func() time.Time {
		now := at(step)
		step++
		return now
	}
}

// plugins are the two shapes a session store can sit on: a tree, where a
// session is a directory something has to make, and a keyspace, where it is a
// prefix that exists because its messages are under it. Every verb is asked on
// both, because which one a mount is holds no interest above the port.
var plugins = map[string]func() vfs.FileSystem{
	"tree":     func() vfs.FileSystem { return memfs.New() },
	"keyspace": func() vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "talk") },
}

func newStore(t *testing.T, plugin func() vfs.FileSystem) (*session.Store, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	store, err := session.NewStore(table, root, ticking())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, table
}

// onBoth runs one case against every plugin, so a promise kept over a tree and
// broken over a keyspace fails rather than passing on the half that was tried.
func onBoth(t *testing.T, run func(t *testing.T, store *session.Store, fsys vfs.FileSystem)) {
	t.Helper()
	for name, plugin := range plugins {
		t.Run(name, func(t *testing.T) {
			store, fsys := newStore(t, plugin)
			run(t, store, fsys)
		})
	}
}

func mustCreate(t *testing.T, store *session.Store, s workspace.Scope, name string, meta session.Meta) {
	t.Helper()
	if err := store.Create(t.Context(), s, name, meta); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
}

func mustAppend(t *testing.T, store *session.Store, s workspace.Scope, name, role, body string) {
	t.Helper()
	if err := store.Append(t.Context(), s, name, role, body); err != nil {
		t.Fatalf("Append(%q, %q): %v", name, body, err)
	}
}

func mustArchive(t *testing.T, store *session.Store, s workspace.Scope, name string, through int, summary string) {
	t.Helper()
	if err := store.Archive(t.Context(), s, name, through, summary); err != nil {
		t.Fatalf("Archive(%q, %d): %v", name, through, err)
	}
}

func mustArchived(t *testing.T, store *session.Store, s workspace.Scope, name string) []session.Message {
	t.Helper()
	got, err := store.Archived(t.Context(), s, name)
	if err != nil {
		t.Fatalf("Archived(%q): %v", name, err)
	}
	return got
}

func mustGet(t *testing.T, store *session.Store, s workspace.Scope, name string) session.Transcript {
	t.Helper()
	got, err := store.Get(t.Context(), s, name)
	if err != nil {
		t.Fatalf("Get(%q): %v", name, err)
	}
	return got
}

// said flattens a transcript into who said what, so a case can state the
// conversation it expects without spelling out sequence numbers or stamps.
func said(got session.Transcript) []string {
	turns := make([]string, 0, len(got.Messages))
	for _, m := range got.Messages {
		turns = append(turns, m.Role+": "+m.Body)
	}
	return turns
}

// turns flattens messages into who said what, which is what the archive answers
// with: a session, unlike a transcript, is not part of the answer.
func turns(messages []session.Message) []string {
	said := make([]string, 0, len(messages))
	for _, m := range messages {
		said = append(said, m.Role+": "+m.Body)
	}
	return said
}

func names(sessions []session.Session) []string {
	found := make([]string, 0, len(sessions))
	for _, s := range sessions {
		found = append(found, s.Name)
	}
	return found
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

// TestASessionIsItsMetadataAndItsTranscript is the ticket: create, list, get,
// delete, append, over both shapes a mount can be.
func TestASessionIsItsMetadataAndItsTranscript(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", session.Meta{"model": "opus", "title": "the hunt"})
		mustAppend(t, store, here, "monday", "user", "where did it go")
		mustAppend(t, store, here, "monday", "assistant", "east")

		listed, err := store.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !equal(names(listed), []string{"monday"}) {
			t.Fatalf("List = %v, want [monday]", names(listed))
		}
		if listed[0].Meta["title"] != "the hunt" {
			t.Errorf("listed metadata = %v, want title %q", listed[0].Meta, "the hunt")
		}

		got := mustGet(t, store, here, "monday")
		if got.Session.Name != "monday" || got.Session.Meta["model"] != "opus" {
			t.Errorf("Get session = %+v, want monday carrying its metadata", got.Session)
		}
		want := []string{"user: where did it go", "assistant: east"}
		if !equal(said(got), want) {
			t.Fatalf("Get transcript = %v, want %v", said(got), want)
		}

		if err := store.Delete(t.Context(), here, "monday"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(t.Context(), here, "monday"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Get after Delete = %v, want fs.ErrNotExist", err)
		}
		listed, err = store.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List after Delete: %v", err)
		}
		if len(listed) != 0 {
			t.Fatalf("List after Delete = %v, want none", names(listed))
		}
	})
}

// TestTranscriptKeepsTheOrderItWasSaidIn appends past nine, where the order the
// names sort in stops being the order they were said in. What a lost sort
// breaks first is not a jumbled read but a lost turn: Append takes the next
// sequence from the last element it saw.
func TestTranscriptKeepsTheOrderItWasSaidIn(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "long", nil)
		want := make([]string, 0, 13)
		for i := 1; i <= 13; i++ {
			body := fmt.Sprintf("turn %d", i)
			mustAppend(t, store, here, "long", "user", body)
			want = append(want, "user: "+body)
		}

		got := mustGet(t, store, here, "long")
		if !equal(said(got), want) {
			t.Fatalf("transcript = %v, want %v", said(got), want)
		}
		for i, m := range got.Messages {
			if m.Seq != i+1 {
				t.Fatalf("message %d has sequence %d, want %d", i, m.Seq, i+1)
			}
		}
	})
}

// TestAppendedTurnsAreStampedAndNeverRewritten is the append-only promise: what
// a turn says and when it was said do not change because a later turn arrived.
func TestAppendedTurnsAreStampedAndNeverRewritten(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		mustAppend(t, store, here, "monday", "user", "first")
		before := mustGet(t, store, here, "monday").Messages

		mustAppend(t, store, here, "monday", "assistant", "second")
		after := mustGet(t, store, here, "monday").Messages

		if len(after) != 2 {
			t.Fatalf("after appending twice there are %d messages, want 2", len(after))
		}
		if after[0] != before[0] {
			t.Fatalf("first message changed from %+v to %+v", before[0], after[0])
		}
		if !after[0].At.Before(after[1].At) {
			t.Errorf("stamps %v and %v are not in the order the turns were said", after[0].At, after[1].At)
		}
	})
}

// TestCreationIsStamped proves the record carries when the session opened,
// which is what tells a cold conversation from a live one.
func TestCreationIsStamped(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		got := mustGet(t, store, here, "monday")
		if !got.Session.Created.Equal(at(0)) {
			t.Fatalf("Created = %v, want %v", got.Session.Created, at(0))
		}
	})
}

// TestASecondSessionCannotTakeATakenName: reopening would interleave two
// conversations into one transcript.
func TestASecondSessionCannotTakeATakenName(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", session.Meta{"model": "opus"})
		mustAppend(t, store, here, "monday", "user", "first")

		err := store.Create(t.Context(), here, "monday", session.Meta{"model": "haiku"})
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("Create over a taken name = %v, want fs.ErrExist", err)
		}
		got := mustGet(t, store, here, "monday")
		if got.Session.Meta["model"] != "opus" || len(got.Messages) != 1 {
			t.Fatalf("refused Create changed the session to %+v with %d messages", got.Session, len(got.Messages))
		}
	})
}

// TestAppendingToNoSessionIsRefused: a session is not created by talking into
// it, or a misspelled name becomes a conversation with no record of whose it is.
func TestAppendingToNoSessionIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		err := store.Append(t.Context(), here, "never-opened", "user", "hello")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Append to no session = %v, want fs.ErrNotExist", err)
		}
		listed, err := store.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(listed) != 0 {
			t.Fatalf("refused Append left %v behind", names(listed))
		}
	})
}

// TestOneWorkspaceNeverSeesAnother is the isolation the scope buys: the same
// name in two scopes is two conversations, and neither is listed by the other.
func TestOneWorkspaceNeverSeesAnother(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		for _, s := range []workspace.Scope{here, there, other} {
			mustCreate(t, store, s, "monday", nil)
			mustAppend(t, store, s, "monday", "user", s.User+"/"+s.Workspace)
		}

		for _, s := range []workspace.Scope{here, there, other} {
			listed, err := store.List(t.Context(), s)
			if err != nil {
				t.Fatalf("List(%v): %v", s, err)
			}
			if !equal(names(listed), []string{"monday"}) {
				t.Fatalf("List(%v) = %v, want [monday]", s, names(listed))
			}
			want := []string{"user: " + s.User + "/" + s.Workspace}
			if got := said(mustGet(t, store, s, "monday")); !equal(got, want) {
				t.Fatalf("Get(%v) = %v, want %v", s, got, want)
			}
		}

		if err := store.Delete(t.Context(), here, "monday"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		for _, s := range []workspace.Scope{there, other} {
			if _, err := store.Get(t.Context(), s, "monday"); err != nil {
				t.Fatalf("Get(%v) after deleting another scope's session: %v", s, err)
			}
		}
	})
}

// TestUserScopeIsRefused: a scope with no workspace derives the path holding
// every workspace that user has, and a session created there would be listed as
// a workspace.
func TestUserScopeIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		user := workspace.Scope{User: "ada"}
		ctx := t.Context()
		for what, err := range map[string]error{
			"Create": store.Create(ctx, user, "monday", nil),
			"Append": store.Append(ctx, user, "monday", "user", "hello"),
			"Delete": store.Delete(ctx, user, "monday"),
		} {
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s in user scope = %v, want fs.ErrInvalid", what, err)
			}
		}
		if _, err := store.Get(ctx, user, "monday"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Get in user scope = %v, want fs.ErrInvalid", err)
		}
		if _, err := store.List(ctx, user); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("List in user scope = %v, want fs.ErrInvalid", err)
		}
	})
}

// TestNamesThatWouldLeaveTheScopeAreRefused, before anything is written.
func TestNamesThatWouldLeaveTheScopeAreRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		for _, name := range []string{"", ".", "..", "a/b", "../escape"} {
			if err := store.Create(t.Context(), here, name, nil); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Create(%q) = %v, want fs.ErrInvalid", name, err)
			}
		}
	})
}

// TestATurnWithNoSpeakerIsRefused: what is extracted from a transcript turns on
// who said what.
func TestATurnWithNoSpeakerIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		if err := store.Append(t.Context(), here, "monday", "", "hello"); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Append with no role = %v, want fs.ErrInvalid", err)
		}
		if got := mustGet(t, store, here, "monday"); len(got.Messages) != 0 {
			t.Fatalf("refused Append left %v behind", said(got))
		}
	})
}

// TestMetadataWithAnUnnamedKeyIsRefused: a value under an empty key is state
// nothing can ask for.
func TestMetadataWithAnUnnamedKeyIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		err := store.Create(t.Context(), here, "monday", session.Meta{"": "nowhere"})
		if !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Create with an empty metadata key = %v, want fs.ErrInvalid", err)
		}
	})
}

// TestDeletingWhatIsNotThereIsNotAnError, so a delete that failed halfway is
// completed by running it again.
func TestDeletingWhatIsNotThereIsNotAnError(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		if err := store.Delete(t.Context(), here, "never-opened"); err != nil {
			t.Fatalf("Delete of no session: %v", err)
		}
	})
}

// TestListingPassesOverWhatIsNotASession, and fails on one that is broken. A
// short list reads as a correct one, so a record that will not decode has to
// stop the call rather than shorten it.
func TestListingPassesOverWhatIsNotASession(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, fsys vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		base, err := here.Path(root, "")
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if err := fsys.Write(t.Context(), path.Join(base, "stray"), []byte("not a session")); err != nil {
			t.Fatalf("Write stray: %v", err)
		}

		listed, err := store.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !equal(names(listed), []string{"monday"}) {
			t.Fatalf("List = %v, want [monday]", names(listed))
		}

		if err := fsys.Write(t.Context(), path.Join(base, "monday", "SESSION"), []byte("{")); err != nil {
			t.Fatalf("Write broken record: %v", err)
		}
		if _, err := store.List(t.Context(), here); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("List over a broken record = %v, want fs.ErrInvalid", err)
		}
	})
}

// TestABrokenTurnFailsTheRead: a transcript missing a turn reads as a
// conversation that was never had.
func TestABrokenTurnFailsTheRead(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, fsys vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		mustAppend(t, store, here, "monday", "user", "first")
		base, err := here.Path(root, "")
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if err := fsys.Write(t.Context(), path.Join(base, "monday", "1"), []byte("{}")); err != nil {
			t.Fatalf("Write broken message: %v", err)
		}
		if _, err := store.Get(t.Context(), here, "monday"); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Get over a message with no speaker = %v, want fs.ErrInvalid", err)
		}
	})
}

// TestListingNoWorkspaceIsEmptyRatherThanMissing: a workspace nobody has talked
// in has no sessions, which is an answer and not a failure.
func TestListingNoWorkspaceIsEmptyRatherThanMissing(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		listed, err := store.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(listed) != 0 {
			t.Fatalf("List = %v, want none", names(listed))
		}
	})
}

// TestAStoreIsWhereAMountWasPut: a root that is not a rooted clean path names
// no mount.
func TestAStoreIsWhereAMountWasPut(t *testing.T) {
	for _, bad := range []string{"", "sessions", "/sessions/", "/sessions/../talk"} {
		if _, err := session.NewStore(vfs.NewMountTable(), bad, time.Now); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewStore(%q) = %v, want fs.ErrInvalid", bad, err)
		}
	}
}

// TestAColdTranscriptIsMovedAsideWithASummaryRetained is the ticket: the cold
// half of a conversation leaves the transcript, the session says what it said,
// and the messages themselves are still there to be read back.
func TestAColdTranscriptIsMovedAsideWithASummaryRetained(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", session.Meta{"model": "opus"})
		mustAppend(t, store, here, "monday", "user", "where did it go")
		mustAppend(t, store, here, "monday", "assistant", "east")
		mustAppend(t, store, here, "monday", "user", "and now")
		mustAppend(t, store, here, "monday", "assistant", "still east")

		mustArchive(t, store, here, "monday", 2, "it went east")

		got := mustGet(t, store, here, "monday")
		want := []string{"user: and now", "assistant: still east"}
		if !equal(said(got), want) {
			t.Fatalf("transcript after Archive = %v, want %v", said(got), want)
		}
		if got.Session.Summary != "it went east" {
			t.Errorf("Summary = %q, want the summary the archive was given", got.Session.Summary)
		}
		if got.Session.Archived != 2 {
			t.Errorf("Archived = %d, want 2", got.Session.Archived)
		}
		if got.Session.Meta["model"] != "opus" {
			t.Errorf("metadata = %v, want the model the session was created with", got.Session.Meta)
		}

		cold := mustArchived(t, store, here, "monday")
		wantCold := []string{"user: where did it go", "assistant: east"}
		if !equal(turns(cold), wantCold) {
			t.Fatalf("Archived = %v, want %v", turns(cold), wantCold)
		}
		for i, m := range cold {
			if m.Seq != i+1 {
				t.Errorf("archived message %d has sequence %d, want %d", i, m.Seq, i+1)
			}
		}

		listed, err := store.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !equal(names(listed), []string{"monday"}) {
			t.Fatalf("List = %v, want [monday]: an archive is not a session", names(listed))
		}
		if listed[0].Summary != "it went east" {
			t.Errorf("listed Summary = %q, want the retained summary", listed[0].Summary)
		}
	})
}

// TestArchivedSequencesAreNeverHandedOutAgain archives the whole transcript, so
// there is nothing left in the session to count the next turn from. Numbering it
// one again would put a new message under a sequence the archive already holds,
// and the two would be one conversation with two turns claiming to be its first.
func TestArchivedSequencesAreNeverHandedOutAgain(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		mustAppend(t, store, here, "monday", "user", "first")
		mustAppend(t, store, here, "monday", "assistant", "second")
		mustArchive(t, store, here, "monday", 2, "they spoke twice")

		mustAppend(t, store, here, "monday", "user", "third")

		got := mustGet(t, store, here, "monday")
		if len(got.Messages) != 1 {
			t.Fatalf("transcript = %v, want the one turn said since the archive", said(got))
		}
		if got.Messages[0].Seq != 3 {
			t.Fatalf("the turn after archiving two has sequence %d, want 3", got.Messages[0].Seq)
		}
		if cold := mustArchived(t, store, here, "monday"); len(cold) != 2 {
			t.Fatalf("Archived = %v, want the two turns that were moved aside", turns(cold))
		}
	})
}

// TestASecondArchiveExtendsTheFirst: a conversation goes cold twice, and the
// archive holds both halves under the sequences they were said in.
func TestASecondArchiveExtendsTheFirst(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "long", nil)
		for i := 1; i <= 4; i++ {
			mustAppend(t, store, here, "long", "user", fmt.Sprintf("turn %d", i))
		}

		mustArchive(t, store, here, "long", 1, "one turn")
		mustArchive(t, store, here, "long", 3, "three turns")

		got := mustGet(t, store, here, "long")
		if !equal(said(got), []string{"user: turn 4"}) {
			t.Fatalf("transcript = %v, want only the fourth turn", said(got))
		}
		if got.Session.Summary != "three turns" {
			t.Errorf("Summary = %q, want the second archive's, which covers both", got.Session.Summary)
		}
		cold := mustArchived(t, store, here, "long")
		want := []string{"user: turn 1", "user: turn 2", "user: turn 3"}
		if !equal(turns(cold), want) {
			t.Fatalf("Archived = %v, want %v", turns(cold), want)
		}
	})
}

// TestArchivingRefusesWhatNoSummaryCanStandFor: every refusal here is a session
// that would afterwards claim a summary covering something other than what is
// in its archive.
func TestArchivingRefusesWhatNoSummaryCanStandFor(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		mustAppend(t, store, here, "monday", "user", "first")
		mustAppend(t, store, here, "monday", "assistant", "second")

		if err := store.Archive(t.Context(), here, "monday", 2, ""); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Archive with no summary = %v, want fs.ErrInvalid", err)
		}
		if err := store.Archive(t.Context(), here, "monday", 0, "nothing"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Archive through 0 = %v, want fs.ErrInvalid", err)
		}
		if err := store.Archive(t.Context(), here, "monday", 3, "a turn nobody said"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Archive past the last turn = %v, want fs.ErrInvalid", err)
		}

		mustArchive(t, store, here, "monday", 2, "they spoke twice")
		if err := store.Archive(t.Context(), here, "monday", 1, "they spoke once"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Archive below what is archived = %v, want fs.ErrInvalid", err)
		}

		got := mustGet(t, store, here, "monday")
		if got.Session.Summary != "they spoke twice" || got.Session.Archived != 2 {
			t.Fatalf("session = %+v, want the archive the refusals left alone", got.Session)
		}
	})
}

// TestDeletingASessionTakesItsArchive: an archive is part of the conversation,
// so a workspace that deleted one does not still hold half of what was said.
func TestDeletingASessionTakesItsArchive(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		mustAppend(t, store, here, "monday", "user", "first")
		mustArchive(t, store, here, "monday", 1, "one turn")

		if err := store.Delete(t.Context(), here, "monday"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Archived(t.Context(), here, "monday"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Archived after Delete = %v, want fs.ErrNotExist", err)
		}
	})
}

// TestAnUnarchivedSessionHasAnEmptyArchive: the verb answers for every session,
// not only the ones something has been moved out of.
func TestAnUnarchivedSessionHasAnEmptyArchive(t *testing.T) {
	onBoth(t, func(t *testing.T, store *session.Store, _ vfs.FileSystem) {
		mustCreate(t, store, here, "monday", nil)
		mustAppend(t, store, here, "monday", "user", "first")

		if cold := mustArchived(t, store, here, "monday"); len(cold) != 0 {
			t.Fatalf("Archived = %v, want none", turns(cold))
		}
		if _, err := store.Archived(t.Context(), here, "tuesday"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Archived of a session nobody created = %v, want fs.ErrNotExist", err)
		}
	})
}
