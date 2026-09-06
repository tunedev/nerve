package memory_test

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/memory"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

func mustArchive(t *testing.T, store *memory.Store, s workspace.Scope, seqs ...int) {
	t.Helper()
	if err := store.Archive(t.Context(), s, seqs); err != nil {
		t.Fatalf("Archive(%v): %v", seqs, err)
	}
}

func mustArchived(t *testing.T, store *memory.Store, s workspace.Scope) []memory.Memory {
	t.Helper()
	memories, err := store.Archived(t.Context(), s)
	if err != nil {
		t.Fatalf("Archived: %v", err)
	}
	return memories
}

// The acceptance: an archived memory is out of what the workspace answers with
// and is still there to be read back and put back.
func TestAnArchivedMemoryIsRecoverableAndNotDeleted(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin, never)

		mustArchive(t, store, here, 1)

		if want := []string{never.Body}; !slices.Equal(bodies(mustList(t, store, here)), want) {
			t.Errorf("List = %v, want %v", bodies(mustList(t, store, here)), want)
		}
		archived := mustArchived(t, store, here)
		if len(archived) != 1 || archived[0].Seq != 1 || archived[0].Fact != dublin {
			t.Fatalf("Archived = %+v, want the memory at 1 as it was written", archived)
		}

		if err := store.Restore(t.Context(), here, []int{1}); err != nil {
			t.Fatalf("Restore(1): %v", err)
		}
		if want := []string{dublin.Body, never.Body}; !slices.Equal(bodies(mustList(t, store, here)), want) {
			t.Errorf("List after Restore = %v, want %v", bodies(mustList(t, store, here)), want)
		}
		if archived := mustArchived(t, store, here); len(archived) != 0 {
			t.Errorf("Archived after Restore = %+v, want nothing", archived)
		}
	})
}

// A memory keeps the sequence it was written under while it is aside, and the
// log keeps counting past it. A sequence handed out twice would put two facts
// under one name and make every supersession ambiguous.
func TestTheLogCountsPastWhatIsArchived(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin, never)
		mustArchive(t, store, here, 1, 2)

		written := mustAdd(t, store, here, "tuesday", tabs)

		if len(written) != 1 || written[0].Seq != 3 {
			t.Fatalf("Add after archiving the whole log wrote %+v, want one memory at 3", written)
		}
		if err := store.Restore(t.Context(), here, []int{1, 2}); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if want := []int{1, 2, 3}; !slices.Equal(seqsOf(mustList(t, store, here)), want) {
			t.Errorf("List = %v, want %v", seqsOf(mustList(t, store, here)), want)
		}
	})
}

// Archiving is a move and not a rewrite, so what a memory said while it was
// believed is what it says in the archive.
func TestArchivingMovesTheMemoryAsItWas(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, fsys vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)
		file := path.Join(root, here.User, here.Workspace, "1")
		before, err := fsys.Read(t.Context(), file)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}

		mustArchive(t, store, here, 1)

		if _, err := fsys.Read(t.Context(), file); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the archived memory is still in the log: Read = %v, want fs.ErrNotExist", err)
		}
		after, err := fsys.Read(t.Context(), path.Join(root, here.User, here.Workspace, "ARCHIVE", "1"))
		if err != nil {
			t.Fatalf("Read the archive: %v", err)
		}
		if string(after) != string(before) {
			t.Errorf("the archived memory reads %s, want %s", after, before)
		}
	})
}

// A supersession reaching into the archive still describes an interval. The
// memory that replaced an archived one is not broken by the archival, and the
// archived one still says when it stopped being believed.
func TestASupersessionIntoTheArchiveStillClosesTheInterval(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)
		mustSupersede(t, store, here, "tuesday", []int{1}, berlin)

		mustArchive(t, store, here, 1)

		memories := mustList(t, store, here)
		if len(memories) != 1 || memories[0].Seq != 2 {
			t.Fatalf("List = %+v, want the replacing memory alone", memories)
		}
		archived := mustArchived(t, store, here)
		if len(archived) != 1 || archived[0].Until != at(1) {
			t.Fatalf("Archived = %+v, want the memory at 1 running until %v", archived, at(1))
		}
	})
}

// Archiving a memory that is not in the log, and restoring one that is not in
// the archive, are claims about nothing. A caller finds out rather than
// silently setting aside or putting back what it did not name.
func TestArchivingAndRestoringWhatIsNotThereIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)

		if err := store.Archive(t.Context(), here, []int{2}); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Archive(2) = %v, want fs.ErrNotExist", err)
		}
		if err := store.Restore(t.Context(), here, []int{1}); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Restore(1), which was never archived, = %v, want fs.ErrNotExist", err)
		}
		if memories := mustList(t, store, here); len(memories) != 1 {
			t.Errorf("List = %+v, want the one memory still in the log", memories)
		}
	})
}

// A batch naming one memory that is not there moves none of it. Half an
// archival is a workspace that stopped believing some of what a caller asked it
// to set aside, with no record of which half.
func TestABadSequenceArchivesNoneOfTheBatch(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin, never)

		if err := store.Archive(t.Context(), here, []int{1, 9}); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Archive(1, 9) = %v, want fs.ErrNotExist", err)
		}
		if want := []int{1, 2}; !slices.Equal(seqsOf(mustList(t, store, here)), want) {
			t.Errorf("List = %v, want %v", seqsOf(mustList(t, store, here)), want)
		}
	})
}

// Archiving nothing is refused. It reads as a request that did something and
// would do nothing.
func TestArchivingNothingIsRefused(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		if err := store.Archive(t.Context(), here, nil); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Archive(none) = %v, want fs.ErrInvalid", err)
		}
		if err := store.Restore(t.Context(), here, nil); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Restore(none) = %v, want fs.ErrInvalid", err)
		}
	})
}

// A workspace nobody has archived from has an empty archive, which is an answer
// rather than an error.
func TestAnArchiveNobodyHasMovedAnythingIntoIsEmpty(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		if archived := mustArchived(t, store, here); len(archived) != 0 {
			t.Errorf("Archived = %+v, want nothing", archived)
		}
	})
}

// The archive is one workspace's own, and a scope naming no workspace is
// refused here for the reason it is refused everywhere below the root.
func TestTheArchiveIsItsWorkspacesAlone(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)
		mustArchive(t, store, here, 1)

		if archived := mustArchived(t, store, there); len(archived) != 0 {
			t.Errorf("the other workspace holds %+v archived, want nothing", archived)
		}
		if err := store.Archive(t.Context(), workspace.Scope{User: "ada"}, []int{1}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Archive in user scope = %v, want fs.ErrInvalid", err)
		}
	})
}

func seqsOf(memories []memory.Memory) []int {
	seqs := make([]int, len(memories))
	for i, m := range memories {
		seqs[i] = m.Seq
	}
	return seqs
}

// A workspace can be told again what it archived. Dedup asks what the workspace
// believes, and it believes nothing it set aside, so a fact restated after its
// memory was archived is written rather than passed over as a repeat.
func TestAFactRestatedAfterItWasArchivedIsWrittenAgain(t *testing.T) {
	onBoth(t, func(t *testing.T, store *memory.Store, _ vfs.FileSystem) {
		mustAdd(t, store, here, "monday", dublin)
		mustArchive(t, store, here, 1)

		written := mustAdd(t, store, here, "tuesday", dublin)

		if len(written) != 1 || written[0].Seq != 2 {
			t.Fatalf("Add wrote %+v, want the fact again at 2", written)
		}
	})
}
