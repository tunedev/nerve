package sqlfs_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
)

// What separates sqlfs from every plugin before it: the tree is still there
// when the process that wrote it is not. No conformance case can see this,
// because the suite hands each case a filesystem with nothing in it.
func TestATreeSurvivesBeingClosedAndOpenedAgain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tree.db")
	ctx := t.Context()

	first, err := sqlfs.Open(file)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if err := first.Mkdir(ctx, "/jobs/queued"); err != nil {
		t.Fatalf("Mkdir(/jobs/queued) = %v", err)
	}
	if err := first.Write(ctx, "/jobs/queued/first", []byte("work")); err != nil {
		t.Fatalf("Write(/jobs/queued/first) = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}

	second, err := sqlfs.Open(file)
	if err != nil {
		t.Fatalf("Open again = %v", err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	}()

	content, err := second.Read(ctx, "/jobs/queued/first")
	if err != nil {
		t.Fatalf("Read after opening again = %v", err)
	}
	if string(content) != "work" {
		t.Errorf("Read after opening again = %q, want %q", content, "work")
	}
}

// A subtree is matched by comparing a prefix, not with LIKE, and this is the
// path that tells the two apart: "%" is a wildcard to LIKE and an ordinary
// character to an operator naming a directory.
func TestASubtreeMatchIsNotAPattern(t *testing.T) {
	fsys, ctx := open(t), t.Context()
	for _, name := range []string{"/100%", "/100x"} {
		if err := fsys.Mkdir(ctx, name); err != nil {
			t.Fatalf("Mkdir(%q) = %v", name, err)
		}
	}
	if err := fsys.Write(ctx, "/100x/kept", []byte("elsewhere")); err != nil {
		t.Fatalf("Write(/100x/kept) = %v", err)
	}

	if err := fsys.RemoveAll(ctx, "/100%"); err != nil {
		t.Fatalf("RemoveAll(/100%%) = %v", err)
	}

	if _, err := fsys.Stat(ctx, "/100x/kept"); err != nil {
		t.Errorf("Stat(/100x/kept) after removing a sibling = %v, want the entry", err)
	}
}

// A verb reaches a database, so the deadline the port promises to propagate has
// somewhere to arrive. sqlfs is the first plugin where a cancelled context is
// answered by something other than the plugin itself.
func TestACancelledContextStopsAVerb(t *testing.T) {
	fsys := open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := fsys.Write(ctx, "/notes", []byte("text")); !errors.Is(err, context.Canceled) {
		t.Errorf("Write on a cancelled context = %v, want context.Canceled", err)
	}
}

// A refused verb leaves the tree as it was. The rename runs two statements, so
// the failure it can have is a subtree half moved.
func TestARefusedRenameMovesNothing(t *testing.T) {
	fsys, ctx := open(t), t.Context()
	if err := fsys.Mkdir(ctx, "/jobs/queued"); err != nil {
		t.Fatalf("Mkdir(/jobs/queued) = %v", err)
	}
	if err := fsys.Write(ctx, "/jobs/queued/first", []byte("work")); err != nil {
		t.Fatalf("Write(/jobs/queued/first) = %v", err)
	}
	if err := fsys.Mkdir(ctx, "/done"); err != nil {
		t.Fatalf("Mkdir(/done) = %v", err)
	}
	if err := fsys.Write(ctx, "/done/kept", []byte("landed")); err != nil {
		t.Fatalf("Write(/done/kept) = %v", err)
	}

	if err := fsys.Rename(ctx, "/jobs", "/done"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Rename onto a directory with entries in it = %v, want fs.ErrExist", err)
	}

	for _, name := range []string{"/jobs", "/jobs/queued", "/jobs/queued/first", "/done/kept"} {
		if _, err := fsys.Stat(ctx, name); err != nil {
			t.Errorf("Stat(%q) after a refused Rename = %v, want the entry", name, err)
		}
	}
}

// A subtree moves by rewriting a prefix, and every row under the entry has to
// be rewritten with it: one left behind is a row no path reaches, and the
// parent column has to move as well as the path.
func TestRenameRewritesAWholeSubtree(t *testing.T) {
	fsys, ctx := open(t), t.Context()
	if err := fsys.Mkdir(ctx, "/jobs/queued/high"); err != nil {
		t.Fatalf("Mkdir(/jobs/queued/high) = %v", err)
	}
	if err := fsys.Write(ctx, "/jobs/queued/high/first", []byte("work")); err != nil {
		t.Fatalf("Write = %v", err)
	}

	if err := fsys.Rename(ctx, "/jobs", "/done"); err != nil {
		t.Fatalf("Rename(/jobs, /done) = %v", err)
	}

	if content, err := fsys.Read(ctx, "/done/queued/high/first"); err != nil || string(content) != "work" {
		t.Errorf("Read(/done/queued/high/first) = %q, %v, want %q", content, err, "work")
	}
	entries, err := fsys.ReadDir(ctx, "/done/queued")
	if err != nil {
		t.Fatalf("ReadDir(/done/queued) = %v", err)
	}
	listed := make([]string, len(entries))
	for i, e := range entries {
		listed[i] = e.Name
	}
	if !slices.Equal(listed, []string{"high"}) {
		t.Errorf("ReadDir(/done/queued) = %v, want [high]; the parent column moved with the path", listed)
	}
	if _, err := fsys.Stat(ctx, "/jobs/queued/high/first"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of the old path = %v, want fs.ErrNotExist", err)
	}
}
