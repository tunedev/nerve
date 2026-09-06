package memory_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The acceptance: a fact stated again does not become a second memory. The one
// the workspace kept is the first statement of it, so the memory names the
// conversation the claim was first made in.
func TestARestatedFactDoesNotBecomeASecondMemory(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs, never)

		written := mustAdd(t, store, here, "tuesday", tabs, dublin)
		if len(written) != 1 || written[0].Fact != dublin {
			t.Fatalf("Add wrote %+v, want only the fact the workspace had not heard", written)
		}

		memories := mustList(t, store, here)
		if len(memories) != 3 {
			t.Fatalf("List = %+v, want three memories", memories)
		}
		if memories[0].Fact != tabs || memories[0].Source != "monday" {
			t.Errorf("memory 1 = %+v from %q, want the first statement, from monday", memories[0].Fact, memories[0].Source)
		}
	})
}

// A conversation that only said again what the workspace already believes
// writes nothing, and that is an answer rather than a failure.
func TestAConversationThatSaidNothingNewWritesNothing(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs, never)

		written := mustAdd(t, store, here, "tuesday", never, tabs)
		if len(written) != 0 {
			t.Errorf("Add wrote %+v, want nothing", written)
		}
		if memories := mustList(t, store, here); len(memories) != 2 {
			t.Errorf("List = %+v, want the two memories the first conversation left", memories)
		}
	})
}

// A batch that restates itself writes one memory, not one per statement. The
// dedup is over the batch and what is believed together, so a transcript that
// said something three times is not three memories the moment nothing was
// stored before it.
func TestABatchThatRestatesItselfWritesOneMemory(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		written := mustAdd(t, store, here, "monday", tabs, never, tabs)

		if len(written) != 2 || written[0].Fact != tabs || written[1].Fact != never {
			t.Errorf("Add wrote %+v, want each claim once, in the order it was first stated", written)
		}
	})
}

// A claim is the category and the body together. The same sentence filed as a
// preference and as an instruction is a habit and an order, and a workspace
// asked for its instructions must not answer with something nobody told it.
func TestTheSameBodyUnderAnotherCategoryIsAnotherClaim(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", memory.Fact{Kind: "preference", Body: "use tabs", Priority: memory.Normal})

		written := mustAdd(t, store, here, "tuesday", memory.Fact{Kind: "instruction", Body: "use tabs", Priority: memory.High})
		if len(written) != 1 {
			t.Errorf("Add wrote %+v, want the instruction as its own memory", written)
		}
	})
}

// A body is read as what it says and not as how it was typed, so a restatement
// differing only in capitalisation or spacing is still a restatement.
func TestASpellingDifferenceIsNotANewClaim(t *testing.T) {
	restated := map[string]string{
		"capitalisation": "Prefers Tabs",
		"spacing":        "prefers   tabs",
		"surrounding":    "  prefers tabs\n",
	}
	for name, body := range restated {
		t.Run(name, func(t *testing.T) {
			onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
				mustAdd(t, store, here, "monday", tabs)

				written := mustAdd(t, store, here, "tuesday", memory.Fact{Kind: tabs.Kind, Body: body, Priority: tabs.Priority})
				if len(written) != 0 {
					t.Errorf("Add(%q) wrote %+v, want nothing", body, written)
				}
			})
		})
	}
}

// A belief the workspace gave up is not a duplicate of anything. Stating it
// again is the belief returning, and folding it into the memory that no longer
// stands would make the log say the workspace never changed its mind.
func TestAFactRestatingASupersededMemoryIsWritten(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)
		berlin := memory.Fact{Kind: "identity", Body: "lives in Berlin", Priority: memory.Low}
		mustSupersede(t, store, here, "tuesday", []int{1}, berlin)

		written := mustAdd(t, store, here, "wednesday", dublin)
		if len(written) != 1 || written[0].Seq != 3 {
			t.Fatalf("Add wrote %+v, want the returned belief at 3", written)
		}
		current, err := store.Current(t.Context(), here)
		if err != nil {
			t.Fatalf("Current: %v", err)
		}
		if len(current) != 2 || bodies(current)[1] != dublin.Body {
			t.Errorf("Current = %+v, want Berlin and Dublin believed again", bodies(current))
		}
	})
}

// A batch carrying one fact that cannot be stored is refused whole, even when
// everything else in it was a restatement that would have been dropped. The
// filter decides what is worth writing, never what is legal.
func TestABadFactIsRefusedEvenAmongRestatements(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs)

		_, err := store.Add(t.Context(), here, "tuesday", []memory.Fact{tabs, {Kind: "identity", Body: "", Priority: memory.Low}})
		if !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Add(a fact with no body) = %v, want fs.ErrInvalid", err)
		}
		if memories := mustList(t, store, here); len(memories) != 1 {
			t.Errorf("List = %+v, want only what the first conversation wrote", memories)
		}
	})
}

// Superseding is an explicit act naming what it replaces, so it writes what it
// was given. A caller that says which memory stops being believed is not
// restating anything.
func TestSupersedingWritesEvenARestatement(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin, tabs)

		written := mustSupersede(t, store, here, "tuesday", []int{1}, tabs)
		if len(written) != 1 || written[0].Seq != 3 {
			t.Errorf("Supersede wrote %+v, want the replacement at 3", written)
		}
	})
}
