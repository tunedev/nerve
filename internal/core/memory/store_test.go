package memory_test

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/memories"

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}
)

// epoch is where the fake clock starts, and each stamp is one second later, so
// a case can say which write produced it without spelling out a time.
var epoch = time.Date(2026, time.September, 3, 9, 0, 0, 0, time.UTC)

func at(step int) time.Time { return epoch.Add(time.Duration(step) * time.Second) }

func ticking() func() time.Time {
	step := 0
	return func() time.Time {
		now := at(step)
		step++
		return now
	}
}

// plugins are the two shapes a memory log can sit on: a tree, where the log is
// a directory something has to make, and a keyspace, where it is a prefix that
// exists because its memories are under it.
var plugins = map[string]func() vfs.FileSystem{
	"tree":     func() vfs.FileSystem { return memfs.New() },
	"keyspace": func() vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "recall") },
}

func newStore(t *testing.T, plugin func() vfs.FileSystem) (*memory.Store, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	store, err := memory.NewStore(table, root, ticking())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, table
}

// onBoth runs one case against every plugin, so a promise kept over a tree and
// broken over a keyspace fails rather than passing on the half that was tried.
func onBoth(t *testing.T, run func(t *testing.T, store *memory.Store, fsys vfs.FileSystem)) {
	t.Helper()
	for name, plugin := range plugins {
		t.Run(name, func(t *testing.T) {
			store, fsys := newStore(t, plugin)
			run(t, store, fsys)
		})
	}
}

func mustAdd(t *testing.T, store *memory.Store, s workspace.Scope, source string, facts ...memory.Fact) []memory.Memory {
	t.Helper()
	written, err := store.Add(t.Context(), s, source, facts)
	if err != nil {
		t.Fatalf("Add(%q): %v", source, err)
	}
	return written
}

func mustList(t *testing.T, store *memory.Store, s workspace.Scope) []memory.Memory {
	t.Helper()
	memories, err := store.List(t.Context(), s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return memories
}

var (
	tabs   = memory.Fact{Kind: "preference", Body: "prefers tabs", Priority: memory.Normal}
	never  = memory.Fact{Kind: "instruction", Body: "never force push", Priority: memory.High}
	dublin = memory.Fact{Kind: "identity", Body: "lives in Dublin", Priority: memory.Low}
)

// The acceptance: what a conversation said comes back as typed, ranked facts,
// each naming the session it was said in, in the order they were written.
func TestAMemoryIsATypedRankedFactNamingItsSession(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		written := mustAdd(t, store, here, "monday", tabs, never)
		if len(written) != 2 || written[0].Seq != 1 || written[1].Seq != 2 {
			t.Fatalf("Add wrote %+v, want two memories at 1 and 2", written)
		}

		memories := mustList(t, store, here)
		if len(memories) != 2 {
			t.Fatalf("List = %d memories, want 2", len(memories))
		}
		if memories[0].Fact != tabs || memories[1].Fact != never {
			t.Errorf("List = %+v, want the two facts in the order they were added", memories)
		}
		for _, m := range memories {
			if m.Source != "monday" {
				t.Errorf("memory %d came from %q, want monday", m.Seq, m.Source)
			}
		}
		if memories[0].At != at(0) || memories[1].At != at(1) {
			t.Errorf("stamps = %v and %v, want %v and %v", memories[0].At, memories[1].At, at(0), at(1))
		}
	})
}

// A second commit continues the log rather than restarting it, and a memory
// already written is never rewritten. What is derived from a memory is derived
// from the memory that was there.
func TestASecondCommitContinuesTheLog(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs)
		written := mustAdd(t, store, here, "tuesday", never)

		if len(written) != 1 || written[0].Seq != 2 {
			t.Fatalf("Add wrote %+v, want one memory at 2", written)
		}
		memories := mustList(t, store, here)
		if len(memories) != 2 || memories[0].Source != "monday" || memories[1].Source != "tuesday" {
			t.Errorf("List = %+v, want monday's memory then tuesday's", memories)
		}
	})
}

// A conversation with nothing worth remembering in it is the ordinary case. It
// writes nothing and is not a failed commit.
func TestAddingNothingWritesNothing(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		written, err := store.Add(t.Context(), here, "monday", nil)
		if err != nil {
			t.Fatalf("Add(none): %v", err)
		}
		if len(written) != 0 {
			t.Errorf("Add(none) wrote %+v, want nothing", written)
		}
		if memories := mustList(t, store, here); len(memories) != 0 {
			t.Errorf("List = %+v, want nothing", memories)
		}
	})
}

// One workspace's memories are not another's, and not another user's. A fact a
// user stated in one workspace turning up in someone else's context is the
// isolation failure this system cannot have.
func TestMemoriesAreTheirWorkspacesAlone(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs)

		if memories := mustList(t, store, there); len(memories) != 0 {
			t.Errorf("the other workspace holds %+v, want nothing", memories)
		}
		if memories := mustList(t, store, other); len(memories) != 0 {
			t.Errorf("the other user holds %+v, want nothing", memories)
		}
	})
}

// A batch carrying one fact that cannot be stored leaves the log as it found
// it. A workspace holding some of what a conversation said, and no record of
// which half, is worse than one holding none of it.
func TestABadFactWritesNoneOfTheBatch(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs)

		_, err := store.Add(t.Context(), here, "tuesday", []memory.Fact{never, {Kind: "", Body: "unasked for", Priority: memory.Low}})
		if !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Add(a fact with no kind) = %v, want fs.ErrInvalid", err)
		}
		memories := mustList(t, store, here)
		if len(memories) != 1 || memories[0].Fact != tabs {
			t.Errorf("List = %+v, want only what the first commit wrote", memories)
		}
	})
}

// A fact off the priority scale is refused. A memory a build cannot rank is one
// it would sort arbitrarily against the rest, which is a caller keeping the
// wrong thing when it has room for one.
func TestAFactOffTheScaleIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		_, err := store.Add(t.Context(), here, "monday", []memory.Fact{{Kind: "preference", Body: "tabs", Priority: 0}})
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Add(priority 0) = %v, want fs.ErrInvalid", err)
		}
	})
}

// A memory that does not say where it came from cannot be checked, reconciled
// or removed with the conversation that produced it.
func TestAMemoryWithoutASourceIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		for _, source := range []string{"", "..", "monday/one"} {
			if _, err := store.Add(t.Context(), here, source, []memory.Fact{tabs}); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Add(source %q) = %v, want fs.ErrInvalid", source, err)
			}
		}
	})
}

// A file standing where a memory belongs and not being one fails the whole
// listing. A short list is indistinguishable from a correct one, and the caller
// is about to build a turn's context out of it.
func TestAMemoryThatWillNotDecodeFailsTheListing(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, fsys vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs, never)

		if err := fsys.Write(t.Context(), path.Join(root, here.User, here.Workspace, "2"), []byte("not a memory")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if _, err := store.List(t.Context(), here); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("List over a broken memory = %v, want fs.ErrInvalid", err)
		}
	})
}

// Anything else a mount put beside the memories is not one. The log's own names
// are the sequences, and a name that does not round-trip to the sequence it
// decodes to would be counted twice.
func TestOnlyMemoriesAreListed(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, fsys vfs.FileSystem) {
		mustAdd(t, store, here, "monday", tabs)

		for _, stray := range []string{"NOTES", "01", "+2"} {
			if err := fsys.Write(t.Context(), path.Join(root, here.User, here.Workspace, stray), []byte("not a memory")); err != nil {
				t.Fatalf("Write(%q): %v", stray, err)
			}
		}
		if memories := mustList(t, store, here); len(memories) != 1 {
			t.Errorf("List = %+v, want the one memory", memories)
		}
	})
}

// A scope naming no workspace is refused. It derives the path holding every
// workspace that user has, so a memory written there would stand where a
// workspace belongs.
func TestUserScopeIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		user := workspace.Scope{User: "ada"}

		if _, err := store.Add(t.Context(), user, "monday", []memory.Fact{tabs}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Add in user scope = %v, want fs.ErrInvalid", err)
		}
		if _, err := store.List(t.Context(), user); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("List in user scope = %v, want fs.ErrInvalid", err)
		}
	})
}

// A store is where a mount was put, so a root that is not a mount table prefix
// is refused at the one place that can still say so.
func TestARootThatIsNotAPrefixIsRefused(t *testing.T) {
	for _, bad := range []string{"memories", "/memories/", "/memories/../x"} {
		if _, err := memory.NewStore(memfs.New(), bad, time.Now); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewStore(%q) = %v, want fs.ErrInvalid", bad, err)
		}
	}
}

// A priority is written by name, so inserting a step between two of them later
// cannot silently re-rank every memory already stored. A name off the scale is
// refused rather than read as the value it sorts next to.
func TestAPriorityIsStoredByName(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, fsys vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)

		data, err := fsys.Read(t.Context(), path.Join(root, here.User, here.Workspace, "1"))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !strings.Contains(string(data), `"priority":"low"`) {
			t.Errorf("the stored memory is %s, want a priority written by name", data)
		}

		if err := fsys.Write(t.Context(), path.Join(root, here.User, here.Workspace, "1"), []byte(`{"fact":{"kind":"preference","body":"tabs","priority":"urgent"},"source":"monday","at":"2026-09-03T09:00:00Z"}`)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if _, err := store.List(t.Context(), here); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("List over a priority off the scale = %v, want fs.ErrInvalid", err)
		}
	})
}
