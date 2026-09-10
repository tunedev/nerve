package thread

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Log is the record of every agent run in every workspace on the tree under
// root, reached by the thread name the caller gives inside its own scope.
type Log struct {
	fsys vfs.FileSystem
	root string
}

// NewLog returns the log keeping threads under root on fsys.
//
// root must be a rooted, clean path such as "/threads" — the mount table's rule
// for a prefix, because a log is where a mount was put.
func NewLog(fsys vfs.FileSystem, root string) (*Log, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("thread root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Log{fsys: fsys, root: root}, nil
}

// Start opens the thread named name in s and records that its run is working.
//
// A name already taken is refused with fs.ErrExist rather than reopened. Two
// runs sharing a thread would interleave their output into one record, and
// neither of them is the run anybody asked about.
func (l *Log) Start(ctx context.Context, s workspace.Scope, name string) error {
	held, dir, err := l.dir(s, name)
	if err != nil {
		return fmt.Errorf("start thread: %w", err)
	}
	if _, err := held.Stat(ctx, path.Join(dir, recordName)); err == nil {
		return fmt.Errorf("start thread %q: %w", name, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("start thread %q: %w", name, err)
	}
	if err := writeRun(ctx, held, dir, Run{Status: Running}); err != nil {
		return fmt.Errorf("start thread %q: %w", name, err)
	}
	return nil
}

// Append adds data to the output of the run in s named name, as the next entry.
//
// It refuses a run that has ended, with fs.ErrPermission. Output arriving after
// an ending extends a result a reader has already been told is the whole of it,
// and a reader that read twice would get two different answers to the same
// question.
//
// Each entry is written once, to a path of its own, and nothing already written
// is read back and rewritten. A run interrupted between two appends has lost
// the entry it was making and nothing else.
func (l *Log) Append(ctx context.Context, s workspace.Scope, name string, data []byte) error {
	held, dir, err := l.dir(s, name)
	if err != nil {
		return fmt.Errorf("append to thread: %w", err)
	}
	run, entries, err := readDir(ctx, held, dir)
	if err != nil {
		return fmt.Errorf("append to thread %q: %w", name, err)
	}
	if run.Status != Running {
		return fmt.Errorf("append to thread %q, which is %s: %w", name, run.Status, fs.ErrPermission)
	}
	next := 1
	if len(entries) > 0 {
		next = entries[len(entries)-1] + 1
	}
	if err := held.Write(ctx, path.Join(dir, entryName(next)), data); err != nil {
		return fmt.Errorf("append to thread %q: %w", name, err)
	}
	return nil
}

// Finish records that the run in s named name ended having produced what it
// meant to. What it produced is what it appended; this verb says only that
// there is no more of it.
func (l *Log) Finish(ctx context.Context, s workspace.Scope, name string) error {
	return l.end(ctx, s, name, Run{Status: Finished})
}

// Fail records that the run in s named name ended without producing its output,
// and why. The output it did append stays: a partial result and the reason it
// is partial are more use than either alone.
func (l *Log) Fail(ctx context.Context, s workspace.Scope, name, because string) error {
	if err := validFailure(because); err != nil {
		return fmt.Errorf("fail thread %q: %w", name, err)
	}
	return l.end(ctx, s, name, Run{Status: Failed, Failure: because})
}

// Get returns where the run in s named name stands, without its output. It is
// the cheap question — is there more coming — asked without reading a run's
// worth of entries to answer it.
func (l *Log) Get(ctx context.Context, s workspace.Scope, name string) (Run, error) {
	held, dir, err := l.dir(s, name)
	if err != nil {
		return Run{}, fmt.Errorf("get thread: %w", err)
	}
	run, err := readRun(ctx, held, dir)
	if err != nil {
		return Run{}, fmt.Errorf("get thread %q: %w", name, err)
	}
	run.Name = name
	return run, nil
}

// Read returns the run in s named name together with every entry it has
// produced, in the order it produced them.
//
// The run comes back with the output rather than beside it, because the caller
// this package exists for is one that was not there when the output was made.
// Entries alone would leave it unable to tell a run that stopped from one that
// is still going, and the wrong guess is a truncated result reported as a whole
// one.
func (l *Log) Read(ctx context.Context, s workspace.Scope, name string) (Result, error) {
	held, dir, err := l.dir(s, name)
	if err != nil {
		return Result{}, fmt.Errorf("read thread: %w", err)
	}
	run, seqs, err := readDir(ctx, held, dir)
	if err != nil {
		return Result{}, fmt.Errorf("read thread %q: %w", name, err)
	}
	run.Name = name

	entries := make([]Entry, 0, len(seqs))
	for _, seq := range seqs {
		data, err := held.Read(ctx, path.Join(dir, entryName(seq)))
		if err != nil {
			return Result{}, fmt.Errorf("read thread %q entry %d: %w", name, seq, err)
		}
		entries = append(entries, Entry{Seq: seq, Data: data})
	}
	return Result{Run: run, Entries: entries}, nil
}

// List returns where every run in s stands, sorted by name.
//
// A child that is not a directory, or is one holding no record, is not a thread
// and is passed over: the tree under a mount is a tree, and something else
// standing in it is not a broken thread. Both filters are needed and neither
// subsumes the other — a plain file where a thread would be reports
// fs.ErrNotExist on a keyspace, where a name is a prefix, and fs.ErrInvalid on
// a tree, where a file has nothing under it.
//
// A record that will not decode is a broken thread, and fails the whole call: a
// short list is indistinguishable from a correct one, and the caller is about
// to conclude something about a run it was not told about.
func (l *Log) List(ctx context.Context, s workspace.Scope) ([]Run, error) {
	held, err := l.held(s)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}

	children, err := held.ReadDir(ctx, "/")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}

	runs := make([]Run, 0, len(children))
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		run, err := readRun(ctx, held, path.Join("/", child.Name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list thread %q: %w", child.Name, err)
		}
		run.Name = child.Name
		runs = append(runs, run)
	}
	slices.SortFunc(runs, func(a, b Run) int { return strings.Compare(a.Name, b.Name) })
	return runs, nil
}

// Delete removes the thread named name in s, record and output together. A
// thread that is not there is not an error, so a delete that failed halfway is
// completed by running it again.
func (l *Log) Delete(ctx context.Context, s workspace.Scope, name string) error {
	held, dir, err := l.dir(s, name)
	if err != nil {
		return fmt.Errorf("delete thread: %w", err)
	}
	if err := held.RemoveAll(ctx, dir); err != nil {
		return fmt.Errorf("delete thread %q: %w", name, err)
	}
	return nil
}

// end writes the record of a run that has stopped. A run that has already
// stopped is refused with fs.ErrPermission, so the first ending is the one that
// stands and a late Fail cannot overwrite the Finish a reader has acted on.
func (l *Log) end(ctx context.Context, s workspace.Scope, name string, ended Run) error {
	held, dir, err := l.dir(s, name)
	if err != nil {
		return fmt.Errorf("end thread: %w", err)
	}
	run, err := readRun(ctx, held, dir)
	if err != nil {
		return fmt.Errorf("end thread %q: %w", name, err)
	}
	if run.Status != Running {
		return fmt.Errorf("end thread %q, which is already %s: %w", name, run.Status, fs.ErrPermission)
	}
	if err := writeRun(ctx, held, dir, ended); err != nil {
		return fmt.Errorf("end thread %q: %w", name, err)
	}
	return nil
}

// readRun returns the record standing in dir. A directory with no record in it
// reports fs.ErrNotExist, which is what "no run was started here" is.
func readRun(ctx context.Context, fsys vfs.FileSystem, dir string) (Run, error) {
	data, err := fsys.Read(ctx, path.Join(dir, recordName))
	if err != nil {
		return Run{}, err
	}
	return decodeRun(data)
}

// writeRun replaces the record standing in dir, making the thread's directory
// first — which is what a tree needs and what a keyspace has nothing to do
// about.
func writeRun(ctx context.Context, fsys vfs.FileSystem, dir string, run Run) error {
	if err := fsys.Mkdir(ctx, dir); err != nil {
		return err
	}
	return fsys.Write(ctx, path.Join(dir, recordName), encodeRun(run))
}

// readDir returns the record standing in dir and the sequence of every entry
// beside it, ascending, in one listing rather than one call per question.
//
// The sequences come from the names on the mount rather than from a count the
// record keeps, so a run that was interrupted leaves a log that still says what
// it holds. Anything in the directory that is not an entry — the record, and
// whatever else a mount put there — is passed over.
func readDir(ctx context.Context, fsys vfs.FileSystem, dir string) (Run, []int, error) {
	run, err := readRun(ctx, fsys, dir)
	if err != nil {
		return Run{}, nil, err
	}
	children, err := fsys.ReadDir(ctx, dir)
	if err != nil {
		return Run{}, nil, err
	}
	seqs := make([]int, 0, len(children))
	for _, child := range children {
		if seq, ok := entrySeq(child.Name); ok {
			seqs = append(seqs, seq)
		}
	}
	slices.Sort(seqs)
	return run, seqs, nil
}

// held is the log as a caller inside s sees it: the threads s keeps are names
// in it rather than a path derived down to them.
//
// The view is what makes the scope a boundary rather than a prefix: a name is
// resolved by the subtree the tree beneath hands out, so a link standing at any
// component of the scope path is refused instead of followed. A path derived
// down to the thread and handed to the whole area is resolved by the whole
// area, which follows that link.
//
// User scope is refused here and so everywhere below it. A scope with no
// workspace derives the path holding every workspace that user has, so a thread
// started there would stand where a workspace belongs and be listed as one.
func (l *Log) held(s workspace.Scope) (vfs.FileSystem, error) {
	if s.Workspace == "" {
		return nil, fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.View(l.fsys, l.root)
}

// dir is the log as a caller inside s sees it, and where the thread named name
// stands in it.
func (l *Log) dir(s workspace.Scope, name string) (vfs.FileSystem, string, error) {
	if err := vfs.ValidSegment("thread name", name); err != nil {
		return nil, "", err
	}
	held, err := l.held(s)
	if err != nil {
		return nil, "", err
	}
	return held, path.Join("/", name), nil
}
