package thread_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/thread"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/threads"

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}
)

// plugins are the two shapes a thread log can sit on: a tree, where a thread is
// a directory something has to make, and a keyspace, where it is a prefix that
// exists because its entries are under it. Every verb is asked on both, because
// which one a mount is holds no interest above the port.
var plugins = map[string]func() vfs.FileSystem{
	"tree":     func() vfs.FileSystem { return memfs.New() },
	"keyspace": func() vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "runs") },
}

func newLog(t *testing.T, plugin func() vfs.FileSystem) (*thread.Log, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	log, err := thread.NewLog(table, root)
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	return log, table
}

// onBoth runs one case against every plugin, so a promise kept over a tree and
// broken over a keyspace fails rather than passing on the half that was tried.
func onBoth(t *testing.T, run func(t *testing.T, log *thread.Log, fsys vfs.FileSystem)) {
	t.Helper()
	for name, plugin := range plugins {
		t.Run(name, func(t *testing.T) {
			log, fsys := newLog(t, plugin)
			run(t, log, fsys)
		})
	}
}

func mustStart(t *testing.T, log *thread.Log, s workspace.Scope, name string) {
	t.Helper()
	if err := log.Start(t.Context(), s, name); err != nil {
		t.Fatalf("Start(%q): %v", name, err)
	}
}

func mustAppend(t *testing.T, log *thread.Log, s workspace.Scope, name string, data string) {
	t.Helper()
	if err := log.Append(t.Context(), s, name, []byte(data)); err != nil {
		t.Fatalf("Append(%q, %q): %v", name, data, err)
	}
}

func mustRead(t *testing.T, log *thread.Log, s workspace.Scope, name string) thread.Result {
	t.Helper()
	result, err := log.Read(t.Context(), s, name)
	if err != nil {
		t.Fatalf("Read(%q): %v", name, err)
	}
	return result
}

// output is what a run produced, flattened, so a case can say what it expects
// without spelling out sequence numbers it does not care about.
func output(result thread.Result) []string {
	said := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		said = append(said, string(entry.Data))
	}
	return said
}

func sameStrings(a, b []string) bool {
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

// The ticket's acceptance, asserted directly: the connection that asked for the
// run goes away mid-run, and everything it produced is still there afterwards
// for a caller holding a context of its own.
func TestOutputSurvivesTheRequestingConnection(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		asked, hangUp := context.WithCancel(t.Context())
		if err := log.Start(asked, here, "search"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := log.Append(asked, here, "search", []byte("found 3 roles")); err != nil {
			t.Fatalf("Append: %v", err)
		}
		hangUp()

		result := mustRead(t, log, here, "search")
		if want := []string{"found 3 roles"}; !sameStrings(output(result), want) {
			t.Fatalf("output = %q, want %q", output(result), want)
		}
		if result.Run.Status != thread.Running {
			t.Fatalf("Status = %q, want %q", result.Run.Status, thread.Running)
		}
	})
}

// A run that ends after the connection has gone still records its ending, which
// is the half of the acceptance the read above cannot show: the reader that
// comes back is told the output is complete.
func TestARunEndsAfterTheConnectionHasGone(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		asked, hangUp := context.WithCancel(t.Context())
		if err := log.Start(asked, here, "search"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		hangUp()

		mustAppend(t, log, here, "search", "found 3 roles")
		if err := log.Finish(t.Context(), here, "search"); err != nil {
			t.Fatalf("Finish: %v", err)
		}

		result := mustRead(t, log, here, "search")
		if result.Run.Status != thread.Finished {
			t.Fatalf("Status = %q, want %q", result.Run.Status, thread.Finished)
		}
		if want := []string{"found 3 roles"}; !sameStrings(output(result), want) {
			t.Fatalf("output = %q, want %q", output(result), want)
		}
	})
}

func TestAppendsComeBackInOrder(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		for i := range 12 {
			mustAppend(t, log, here, "search", fmt.Sprintf("chunk %d", i))
		}

		result := mustRead(t, log, here, "search")
		if len(result.Entries) != 12 {
			t.Fatalf("read %d entries, want 12", len(result.Entries))
		}
		for i, entry := range result.Entries {
			if want := fmt.Sprintf("chunk %d", i); string(entry.Data) != want {
				t.Fatalf("entry %d = %q, want %q", i, entry.Data, want)
			}
			if entry.Seq != i+1 {
				t.Fatalf("entry %d has Seq %d, want %d", i, entry.Seq, i+1)
			}
		}
	})
}

// The order is the order of appends and not the order the names sort in.
// Twelve is past the point where the two disagree: "10" sorts ahead of "9", so
// a log that trusted the listing would hand back the entries jumbled and would
// then write the thirteenth over the tenth.
func TestOrderDoesNotFollowTheNameOrder(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		for range 12 {
			mustAppend(t, log, here, "search", "x")
		}

		result := mustRead(t, log, here, "search")
		for i, entry := range result.Entries {
			if entry.Seq != i+1 {
				t.Fatalf("entry at index %d has Seq %d, want %d", i, entry.Seq, i+1)
			}
		}
	})
}

// Output that arrived after the ending would extend a result a reader has
// already been told is whole, so it is refused rather than accepted.
func TestAppendAfterAnEndingIsRefused(t *testing.T) {
	for _, end := range []struct {
		name string
		end  func(log *thread.Log, ctx context.Context) error
	}{
		{"finished", func(log *thread.Log, ctx context.Context) error { return log.Finish(ctx, here, "search") }},
		{"failed", func(log *thread.Log, ctx context.Context) error {
			return log.Fail(ctx, here, "search", "model timed out")
		}},
	} {
		t.Run(end.name, func(t *testing.T) {
			onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
				mustStart(t, log, here, "search")
				if err := end.end(log, t.Context()); err != nil {
					t.Fatalf("ending the run: %v", err)
				}

				err := log.Append(t.Context(), here, "search", []byte("late"))
				if !errors.Is(err, fs.ErrPermission) {
					t.Fatalf("Append after %s = %v, want fs.ErrPermission", end.name, err)
				}
			})
		})
	}
}

// A run that failed keeps what it managed to produce. A partial result and the
// reason it is partial are more use than either alone.
func TestAFailedRunKeepsItsPartialOutput(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		mustAppend(t, log, here, "search", "found 1 role")
		if err := log.Fail(t.Context(), here, "search", "model timed out"); err != nil {
			t.Fatalf("Fail: %v", err)
		}

		result := mustRead(t, log, here, "search")
		if result.Run.Status != thread.Failed {
			t.Fatalf("Status = %q, want %q", result.Run.Status, thread.Failed)
		}
		if result.Run.Failure != "model timed out" {
			t.Fatalf("Failure = %q, want %q", result.Run.Failure, "model timed out")
		}
		if want := []string{"found 1 role"}; !sameStrings(output(result), want) {
			t.Fatalf("output = %q, want %q", output(result), want)
		}
	})
}

// The first ending stands. A late Fail overwriting a Finish would take back an
// answer a reader has already acted on.
func TestASecondEndingIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		if err := log.Finish(t.Context(), here, "search"); err != nil {
			t.Fatalf("Finish: %v", err)
		}

		if err := log.Fail(t.Context(), here, "search", "model timed out"); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("Fail after Finish = %v, want fs.ErrPermission", err)
		}
		run, err := log.Get(t.Context(), here, "search")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if run.Status != thread.Finished {
			t.Fatalf("Status = %q, want %q", run.Status, thread.Finished)
		}
	})
}

// Two runs sharing a thread would interleave their output into one record, and
// neither of them is the run anybody asked about.
func TestStartingATakenNameIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")

		if err := log.Start(t.Context(), here, "search"); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("Start twice = %v, want fs.ErrExist", err)
		}
	})
}

// Get answers the cheap question without reading a run's worth of entries to do
// it.
func TestGetAnswersWithoutTheOutput(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		mustAppend(t, log, here, "search", "found 3 roles")

		run, err := log.Get(t.Context(), here, "search")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if run.Name != "search" || run.Status != thread.Running {
			t.Fatalf("Get = %+v, want a running run named %q", run, "search")
		}
	})
}

func TestGetOfAThreadNobodyStarted(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		_, err := log.Get(t.Context(), here, "search")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Get of an unstarted thread = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestListNamesEveryRunSorted(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		mustStart(t, log, here, "apply")
		if err := log.Finish(t.Context(), here, "apply"); err != nil {
			t.Fatalf("Finish: %v", err)
		}

		runs, err := log.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) != 2 {
			t.Fatalf("List returned %d runs, want 2", len(runs))
		}
		if runs[0].Name != "apply" || runs[0].Status != thread.Finished {
			t.Fatalf("first run = %+v, want apply finished", runs[0])
		}
		if runs[1].Name != "search" || runs[1].Status != thread.Running {
			t.Fatalf("second run = %+v, want search running", runs[1])
		}
	})
}

func TestListOfAWorkspaceWithNoRuns(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		runs, err := log.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) != 0 {
			t.Fatalf("List returned %d runs, want none", len(runs))
		}
	})
}

// Something else standing under the log's root is not a broken thread, whether
// it is a directory holding no record or a plain file where a thread would be.
func TestListPassesOverWhatIsNotAThread(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, fsys vfs.FileSystem) {
		mustStart(t, log, here, "search")
		base, err := here.Path(root, "")
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if err := fsys.Mkdir(t.Context(), path.Join(base, "stray")); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if err := fsys.Write(t.Context(), path.Join(base, "stray", "notes.md"), []byte("hello")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := fsys.Write(t.Context(), path.Join(base, "loose.md"), []byte("hello")); err != nil {
			t.Fatalf("Write: %v", err)
		}

		runs, err := log.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) != 1 || runs[0].Name != "search" {
			t.Fatalf("List = %+v, want only search", runs)
		}
	})
}

// A record that will not decode fails the whole call. A short list is
// indistinguishable from a correct one, and the caller is about to conclude
// something about a run it was not told about.
func TestListFailsOnARecordThatWillNotDecode(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, fsys vfs.FileSystem) {
		mustStart(t, log, here, "search")
		at, err := here.Path(root, path.Join("search", "RUN"))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if err := fsys.Write(t.Context(), at, []byte("halfway")); err != nil {
			t.Fatalf("Write: %v", err)
		}

		if _, err := log.List(t.Context(), here); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("List over a broken record = %v, want fs.ErrInvalid", err)
		}
	})
}

func TestDeleteRemovesTheRecordAndTheOutput(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		mustAppend(t, log, here, "search", "found 3 roles")

		if err := log.Delete(t.Context(), here, "search"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := log.Read(t.Context(), here, "search"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Read after Delete = %v, want fs.ErrNotExist", err)
		}
		runs, err := log.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) != 0 {
			t.Fatalf("List after Delete = %+v, want none", runs)
		}
	})
}

// A delete that failed halfway is completed by running it again.
func TestDeletingAThreadThatIsNotThere(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		if err := log.Delete(t.Context(), here, "search"); err != nil {
			t.Fatalf("Delete of a thread that is not there: %v", err)
		}
	})
}

// A thread is reached by the scope that owns it, so the same name in another
// workspace and in another user's is another run.
func TestAThreadIsOnlyVisibleInsideItsScope(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")
		mustAppend(t, log, here, "search", "found 3 roles")

		for _, elsewhere := range []workspace.Scope{there, other} {
			if _, err := log.Get(t.Context(), elsewhere, "search"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Get from %+v = %v, want fs.ErrNotExist", elsewhere, err)
			}
			runs, err := log.List(t.Context(), elsewhere)
			if err != nil {
				t.Fatalf("List from %+v: %v", elsewhere, err)
			}
			if len(runs) != 0 {
				t.Fatalf("List from %+v = %+v, want none", elsewhere, runs)
			}
		}
	})
}

// A scope with no workspace derives the path holding every workspace that user
// has, so a thread started there would be listed as a workspace.
func TestUserScopeIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		user := workspace.Scope{User: "ada"}

		if err := log.Start(t.Context(), user, "search"); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Start at user scope = %v, want fs.ErrInvalid", err)
		}
		if _, err := log.List(t.Context(), user); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("List at user scope = %v, want fs.ErrInvalid", err)
		}
	})
}

// A name carrying a separator names a run inside another one.
func TestANameThatIsNotOneSegmentIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		for _, name := range []string{"", "runs/search", "..", "."} {
			if err := log.Start(t.Context(), here, name); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("Start(%q) = %v, want fs.ErrInvalid", name, err)
			}
		}
	})
}

// A failed run that gives no reason, or one spanning two lines, would write a
// record that does not read back as the run it describes.
func TestAFailureReasonMustBeOneLine(t *testing.T) {
	onBoth(t, func(t *testing.T, log *thread.Log, _ vfs.FileSystem) {
		mustStart(t, log, here, "search")

		for _, because := range []string{"", "timed out\nand gave up"} {
			if err := log.Fail(t.Context(), here, "search", because); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("Fail(%q) = %v, want fs.ErrInvalid", because, err)
			}
		}
	})
}

func TestNewLogRefusesARootThatIsNotAMountPrefix(t *testing.T) {
	for _, bad := range []string{"threads", "/threads/", "/threads/../threads", ""} {
		if _, err := thread.NewLog(memfs.New(), bad); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("NewLog(%q) = %v, want fs.ErrInvalid", bad, err)
		}
	}
}
