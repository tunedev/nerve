package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Log is where the jobs that have started and not finished are written down,
// kept on the tree under root and reached by the job's name inside its own
// scope.
//
// It is a tree rather than a file because the verb set has no append: an object
// store cannot add a line to anything, and a log only a disk could hold would
// not be a log every mount can keep. One note per job is what a store with
// Write and Remove can do, and it is all the log is ever asked: whether this
// job is owed.
//
// Nothing here runs a job or knows what one does.
type Log struct {
	fsys vfs.FileSystem
	root string
	now  func() time.Time
}

// NewLog returns the log keeping its notes under root on fsys, stamping each
// one with now.
//
// root must be a rooted, clean path such as "/schedule" — the mount table's
// rule for a prefix, because a log is where a mount was put. It is its own root
// rather than a corner of the tree the jobs derive from: a note written there
// would be published to whatever watches that tree, and the write that queued
// the job would queue the note as well.
func NewLog(fsys vfs.FileSystem, root string, now func() time.Time) (*Log, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("schedule root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Log{fsys: fsys, root: root, now: now}, nil
}

// Begin records that job is about to run, replacing the note already standing
// for it.
//
// It returns only once the note is stored, which is the ordering the whole
// thing rests on: a caller that ran the job first would have nothing written
// down for exactly the window it cannot survive.
func (l *Log) Begin(ctx context.Context, job Job) error {
	held, at, err := l.at(job)
	if err != nil {
		return fmt.Errorf("begin job: %w", err)
	}
	data, err := json.Marshal(note{At: l.now()})
	if err != nil {
		return fmt.Errorf("begin job %q: %w", job.Name, err)
	}
	if err := held.Mkdir(ctx, path.Dir(at)); err != nil {
		return fmt.Errorf("begin job %q: %w", job.Name, err)
	}
	if err := held.Write(ctx, at, data); err != nil {
		return fmt.Errorf("begin job %q: %w", job.Name, err)
	}
	return nil
}

// End clears the note standing for job, which is what says the run finished.
//
// A job with no note is not an error. Ending twice is the same claim made
// twice, and the log holds no history for the second one to contradict.
func (l *Log) End(ctx context.Context, job Job) error {
	held, at, err := l.at(job)
	if err != nil {
		return fmt.Errorf("end job: %w", err)
	}
	if err := held.Remove(ctx, at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("end job %q: %w", job.Name, err)
	}
	return nil
}

// Unfinished returns every job the log still holds, oldest first, which is the
// order the runs began in and so the order they were wanted in.
//
// A root with nothing under it is a process that has never begun a job, or one
// that finished everything it began. Both are an empty answer rather than a
// refusal.
func (l *Log) Unfinished(ctx context.Context) ([]Job, error) {
	held, err := l.walk(ctx, l.root)
	if err != nil {
		return nil, fmt.Errorf("unfinished jobs: %w", err)
	}
	slices.SortFunc(held, func(a, b owed) int {
		if by := a.at.Compare(b.at); by != 0 {
			return by
		}
		return strings.Compare(a.job.Name, b.job.Name)
	})
	jobs := make([]Job, len(held))
	for i, one := range held {
		jobs[i] = one.job
	}
	return jobs, nil
}

// walk collects the notes at and below at.
//
// The depth is not spelled here. Where a scope puts a name under a root is
// Scope.Path's answer and reading it back is workspace.Locate's, so this
// descends until it finds files and asks Locate what each one is about; a
// second place that took the layout apart would be a second place that could
// take it apart differently.
func (l *Log) walk(ctx context.Context, at string) ([]owed, error) {
	entries, err := l.fsys.ReadDir(ctx, at)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var held []owed
	for _, entry := range entries {
		below := path.Join(at, entry.Name)
		if entry.IsDir() {
			deeper, err := l.walk(ctx, below)
			if err != nil {
				return nil, err
			}
			held = append(held, deeper...)
			continue
		}
		one, err := l.read(ctx, below)
		if err != nil {
			return nil, err
		}
		held = append(held, one)
	}
	return held, nil
}

// read returns the job the note at stands for and when its run began.
func (l *Log) read(ctx context.Context, at string) (owed, error) {
	s, name, err := workspace.Locate(l.root, at)
	if err != nil {
		return owed{}, fmt.Errorf("note %q: %w", at, err)
	}
	data, err := l.fsys.Read(ctx, at)
	if err != nil {
		return owed{}, fmt.Errorf("note %q: %w", at, err)
	}
	held, err := decode(data)
	if err != nil {
		return owed{}, fmt.Errorf("note %q: %w", at, err)
	}
	return owed{job: Job{Scope: s, Name: name}, at: held.At}, nil
}

// at is the log as a caller inside job's scope sees it, and where the note for
// job stands in it.
//
// The view is what makes the scope a boundary rather than a prefix: the name is
// resolved by the subtree the tree beneath hands out, so a link standing at any
// component of the scope path is refused instead of followed. A path derived
// down to the note and handed to the whole area is resolved by the whole area,
// which follows that link.
//
// User scope is refused: a scope with no workspace derives the path holding
// every workspace that user has, so a note written there would stand where a
// workspace belongs and Locate would not read it back as a job.
func (l *Log) at(job Job) (vfs.FileSystem, string, error) {
	if err := vfs.ValidSegment("job name", job.Name); err != nil {
		return nil, "", err
	}
	if job.Scope.Workspace == "" {
		return nil, "", fmt.Errorf("job %q names no workspace: %w", job.Name, fs.ErrInvalid)
	}
	held, err := job.Scope.View(l.fsys, l.root)
	if err != nil {
		return nil, "", err
	}
	return held, path.Join("/", job.Name), nil
}

// owed is one note read back: the job it stands for, and when its run began.
type owed struct {
	job Job
	at  time.Time
}

// note is what a file under the log holds. The job is not in it, because the
// job is the file's own path: a note carrying one would be a second answer to
// what work it is about.
type note struct {
	At time.Time `json:"at"`
}

// decode reads a file back into the note it holds.
//
// A file standing where a note belongs and not being one is refused with
// fs.ErrInvalid rather than skipped. Skipping it would drop a job silently, and
// a job dropped silently is what this package exists to make impossible.
func decode(data []byte) (note, error) {
	var held note
	if err := json.Unmarshal(data, &held); err != nil {
		return note{}, fmt.Errorf("does not decode: %w: %w", err, fs.ErrInvalid)
	}
	if held.At.IsZero() {
		return note{}, fmt.Errorf("says when no run began: %w", fs.ErrInvalid)
	}
	return held, nil
}
