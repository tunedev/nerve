package schedule_test

import (
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/schedule"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Recovery runs the backlog in the order the runs began, because that is the
// order they were wanted in.
func TestUnfinishedJobsComeBackOldestFirst(t *testing.T) {
	fsys := tree(t)
	later := schedule.Job{Scope: here, Name: "usage"}
	earlier := schedule.Job{Scope: here, Name: "sales"}

	if err := newLog(t, fsys, began.Add(time.Hour)).Begin(t.Context(), later); err != nil {
		t.Fatalf("Begin(%q): %v", later.Name, err)
	}
	log := newLog(t, fsys, began)
	if err := log.Begin(t.Context(), earlier); err != nil {
		t.Fatalf("Begin(%q): %v", earlier.Name, err)
	}

	owed, err := log.Unfinished(t.Context())
	if err != nil {
		t.Fatalf("Unfinished: %v", err)
	}
	want := []schedule.Job{earlier, later}
	if len(owed) != len(want) || owed[0] != want[0] || owed[1] != want[1] {
		t.Errorf("Unfinished = %+v, want %+v", owed, want)
	}
}

// Two runs of one job are the same job, so the second note replaces the first
// rather than adding to a backlog that would then be run twice.
func TestBeginningAJobTwiceLeavesOneNote(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)
	job := schedule.Job{Scope: here, Name: "sales"}

	for range 2 {
		if err := log.Begin(t.Context(), job); err != nil {
			t.Fatalf("Begin: %v", err)
		}
	}

	owed, err := log.Unfinished(t.Context())
	if err != nil {
		t.Fatalf("Unfinished: %v", err)
	}
	if len(owed) != 1 {
		t.Errorf("Unfinished = %+v, want one note", owed)
	}
}

// One workspace's backlog is not another's. The note stands at the path the
// scope derives, which is the only reason recovery in a shared process does not
// run Ada's projection for Bob.
func TestNotesStandInTheWorkspaceTheJobDerivesFrom(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)
	mine := schedule.Job{Scope: here, Name: "sales"}
	theirs := schedule.Job{Scope: workspace.Scope{User: "bob", Workspace: "hunt"}, Name: "sales"}

	for _, job := range []schedule.Job{mine, theirs} {
		if err := log.Begin(t.Context(), job); err != nil {
			t.Fatalf("Begin(%v): %v", job.Scope, err)
		}
	}
	if err := log.End(t.Context(), mine); err != nil {
		t.Fatalf("End: %v", err)
	}

	owed, err := log.Unfinished(t.Context())
	if err != nil {
		t.Fatalf("Unfinished: %v", err)
	}
	if len(owed) != 1 || owed[0] != theirs {
		t.Errorf("Unfinished = %+v, want %+v", owed, theirs)
	}
}

// A log nothing has ever begun a job in is an empty backlog, which is what a
// process starting for the first time asks and gets.
func TestALogWithNothingUnderItIsAnEmptyBacklog(t *testing.T) {
	owed, err := newLog(t, tree(t), began).Unfinished(t.Context())
	if err != nil {
		t.Fatalf("Unfinished: %v", err)
	}
	if len(owed) != 0 {
		t.Errorf("Unfinished = %+v, want none", owed)
	}
}

// Ending a job nothing began is the same claim made twice. The log holds no
// history for the second one to contradict.
func TestEndingAJobWithNoNoteIsNotAnError(t *testing.T) {
	if err := newLog(t, tree(t), began).End(t.Context(), schedule.Job{Scope: here, Name: "sales"}); err != nil {
		t.Errorf("End: %v", err)
	}
}

// A name that is not one flat segment names a note about different work, or one
// inside another note. Both are refused before anything is written.
func TestAJobNameThatIsNotOneSegmentIsRefused(t *testing.T) {
	log := newLog(t, tree(t), began)

	for _, name := range []string{"", "sales/2026", ".", ".."} {
		if err := log.Begin(t.Context(), schedule.Job{Scope: here, Name: name}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Begin(%q) = %v, want %v", name, err, fs.ErrInvalid)
		}
	}
}

// A scope with no workspace derives the path holding every workspace that user
// has. A note written there would stand where a workspace belongs.
func TestAJobThatNamesNoWorkspaceIsRefused(t *testing.T) {
	log := newLog(t, tree(t), began)
	job := schedule.Job{Scope: workspace.Scope{User: "ada"}, Name: "sales"}

	if err := log.Begin(t.Context(), job); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Begin = %v, want %v", err, fs.ErrInvalid)
	}
}

// A file standing where a note belongs and not being one is refused rather than
// skipped: skipping it would drop a job, silently, which is the one thing this
// log exists to prevent.
func TestAFileThatIsNotANoteIsRefusedRatherThanSkipped(t *testing.T) {
	fsys := tree(t)
	log := newLog(t, fsys, began)

	at, err := here.Path(root, "sales")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := fsys.Mkdir(t.Context(), "/schedule/ada/hunt"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fsys.Write(t.Context(), at, []byte("not a note")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := log.Unfinished(t.Context()); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Unfinished = %v, want %v", err, fs.ErrInvalid)
	}
}

// A root that is not a mount prefix is refused at construction, where an
// operator can still read the message, rather than at the first job.
func TestALogRootThatIsNotAMountPrefixIsRefused(t *testing.T) {
	fsys := tree(t)

	for _, bad := range []string{"schedule", "/schedule/", "/schedule/../schedule"} {
		if _, err := schedule.NewLog(fsys, bad, time.Now); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewLog(%q) = %v, want %v", bad, err, fs.ErrInvalid)
		}
	}
}
