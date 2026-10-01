package memory_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/vfs"
)

var (
	livesDublin = memory.Fact{Kind: "identity", Subject: "home", Body: "I live in Dublin", Priority: memory.High}
	livesBerlin = memory.Fact{Kind: "identity", Subject: "home", Body: "I live in Berlin", Priority: memory.High}
	likesTea    = memory.Fact{Kind: "preference", Body: "I like tea", Priority: memory.Normal}
	likesCoffee = memory.Fact{Kind: "preference", Body: "I like coffee", Priority: memory.Normal}
)

// The acceptance: a fact answering a question the workspace already holds an
// answer to replaces that answer rather than standing beside it. One memory is
// believed afterwards, and the one it replaced is still what the workspace
// believed before.
func TestAContradictingFactSupersedesTheBeliefItContradicts(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", livesDublin, never)

		written := mustAdd(t, store, here, "tuesday", livesBerlin)
		if len(written) != 1 || written[0].Seq != 3 || !slices.Equal(written[0].Supersedes, []int{1}) {
			t.Fatalf("Add wrote %+v, want Berlin at 3 replacing 1", written)
		}

		current, err := store.Current(t.Context(), here)
		if err != nil {
			t.Fatalf("Current: %v", err)
		}
		if want := []string{never.Body, livesBerlin.Body}; !slices.Equal(bodies(current), want) {
			t.Errorf("Current = %v, want %v", bodies(current), want)
		}

		then, err := store.AsOf(t.Context(), here, at(1))
		if err != nil {
			t.Fatalf("AsOf: %v", err)
		}
		if want := []string{livesDublin.Body, never.Body}; !slices.Equal(bodies(then), want) {
			t.Errorf("AsOf(%v) = %v, want %v", at(1), bodies(then), want)
		}
	})
}

// A fact naming no subject answers no single question, so nothing it says
// replaces anything. Liking coffee does not stop anyone liking tea.
func TestAFactNamingNoSubjectReplacesNothing(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", likesTea)

		written := mustAdd(t, store, here, "tuesday", likesCoffee)
		if len(written) != 1 || len(written[0].Supersedes) != 0 {
			t.Fatalf("Add wrote %+v, want coffee replacing nothing", written)
		}
		if current := mustCurrent(t, store); len(current) != 2 {
			t.Errorf("Current = %v, want tea and coffee both believed", bodies(current))
		}
	})
}

// The subject is half of the question. The same subject under another category
// is another question, and answering one does not answer the other.
func TestTheSameSubjectUnderAnotherCategoryReplacesNothing(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", livesDublin)

		wish := memory.Fact{Kind: "preference", Subject: "home", Body: "I'd rather live by the sea", Priority: memory.Normal}
		written := mustAdd(t, store, here, "tuesday", wish)
		if len(written) != 1 || len(written[0].Supersedes) != 0 {
			t.Errorf("Add wrote %+v, want the wish replacing nothing", written)
		}
	})
}

// An answer restated is a restatement and not a contradiction of itself, so it
// writes nothing and the memory believed is still the first statement of it.
func TestARestatedAnswerIsNotAContradiction(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", livesDublin)

		if written := mustAdd(t, store, here, "tuesday", livesDublin); len(written) != 0 {
			t.Errorf("Add wrote %+v, want nothing", written)
		}
	})
}

// A conversation that answered one question twice left the workspace with its
// last answer. Only that one is written, so a batch never replaces what it wrote
// a moment earlier.
func TestAConversationsLastAnswerIsTheOneKept(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", livesDublin)

		written := mustAdd(t, store, here, "tuesday", livesBerlin, never, livesDublin)
		if len(written) != 1 || written[0].Fact != never {
			t.Fatalf("Add wrote %+v, want only the instruction: the conversation ended back in Dublin", written)
		}

		written = mustAdd(t, store, here, "wednesday", livesDublin, livesBerlin)
		if len(written) != 1 || written[0].Fact != livesBerlin || !slices.Equal(written[0].Supersedes, []int{1}) {
			t.Errorf("Add wrote %+v, want Berlin alone replacing 1", written)
		}
	})
}

// A batch is refused whole for a fact that cannot be stored, even when a later
// answer to the same question would have left it unwritten.
func TestABadAnswerIsRefusedEvenWhenALaterOneReplacesIt(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		bad := memory.Fact{Kind: "identity", Subject: "home", Body: "I live in Cork"}
		_, err := store.Add(t.Context(), here, "monday", []memory.Fact{bad, livesBerlin})
		if !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Add(a fact off the scale) = %v, want fs.ErrInvalid", err)
		}
		if memories := mustList(t, store, here); len(memories) != 0 {
			t.Errorf("List = %+v, want nothing written", memories)
		}
	})
}

func mustCurrent(t *testing.T, store *memory.Store) []memory.Memory {
	t.Helper()
	current, err := store.Current(t.Context(), here)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	return current
}
