package sqlfs_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/sqlfs"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestFactoryBuildsAUsableTree(t *testing.T) {
	fsys := built(t, filepath.Join(t.TempDir(), "tree.db"))

	ctx := t.Context()
	if err := fsys.Write(ctx, "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}
	content, err := fsys.Read(ctx, "/notes")
	if err != nil {
		t.Fatalf("Read(/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/notes) = %q, want %q", content, "text")
	}
}

// A SQLite mount with nowhere to write is a tree in memory with a driver in the
// way, and boot is where an operator can still name the file.
func TestFactoryRefusesAMountWithNoDatabaseFile(t *testing.T) {
	_, err := sqlfs.Factory(nil)

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(nil) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("Factory(nil) = %v, want the missing setting named", err)
	}
}

// A setting sqlfs cannot honour is a misspelling of one meant for another kind,
// and ignoring it makes the misspelling a silent no-op.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	_, err := sqlfs.Factory(map[string]string{
		"path": filepath.Join(t.TempDir(), "tree.db"),
		"root": "/tmp/nerve",
	})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(path, root) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("Factory(path, root) = %v, want the setting named", err)
	}
}

// Two mounts naming two files are two trees. A mount table is where this stops
// being obvious: both are the same kind and the same factory.
func TestEachFileIsItsOwnTree(t *testing.T) {
	dir, ctx := t.TempDir(), t.Context()
	first := built(t, filepath.Join(dir, "first.db"))
	second := built(t, filepath.Join(dir, "second.db"))

	if err := first.Write(ctx, "/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/notes) = %v", err)
	}

	if _, err := second.Read(ctx, "/notes"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/notes) on the second tree = %v, want fs.ErrNotExist", err)
	}
}

// built makes a tree through the factory and closes it when the case ends.
//
// Close is on the concrete type rather than on the port, so a caller still
// holding the tree calls it. A test is such a caller; a mount built from config
// is not, and what closes that one is the registry's Release.
func built(t *testing.T, file string) vfs.FileSystem {
	t.Helper()

	fsys, err := sqlfs.Factory(map[string]string{"path": file})
	if err != nil {
		t.Fatalf("Factory(%q) = %v", file, err)
	}
	tree, ok := fsys.(*sqlfs.FS)
	if !ok {
		t.Fatalf("Factory(%q) built %T, want *sqlfs.FS", file, fsys)
	}
	t.Cleanup(func() {
		if err := tree.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	return tree
}

// The registry is the only route into the tree, and it is what a config
// document reaches: a kind that cannot be mounted is not a plugin, whatever it
// implements.
//
// The real factory opens the database, so the connection this makes is one no
// caller can reach: the table hands back a wrapper. Release is what gives it
// up, and the goroutine check this binary ends on is what proves it did — a
// database/sql pool left open keeps a connection opener running.
func TestSqlfsAnswersThroughAMountTable(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registry.Register(sqlfs.Kind, sqlfs.Factory, sqlfs.PathSetting); err != nil {
		t.Fatalf("Register(sqlfs) = %v", err)
	}
	t.Cleanup(func() {
		if err := registry.Release(); err != nil {
			t.Errorf("Release = %v", err)
		}
	})

	table, err := registry.Table([]vfs.MountSpec{{
		Prefix:   "/scratch",
		Kind:     sqlfs.Kind,
		Settings: map[string]string{sqlfs.PathSetting: filepath.Join(t.TempDir(), "tree.db")},
	}})
	if err != nil {
		t.Fatalf("Table(/scratch) = %v", err)
	}

	ctx := t.Context()
	if err := table.Write(ctx, "/scratch/notes", []byte("text")); err != nil {
		t.Fatalf("Write(/scratch/notes) = %v", err)
	}
	content, err := table.Read(ctx, "/scratch/notes")
	if err != nil {
		t.Fatalf("Read(/scratch/notes) = %v", err)
	}
	if string(content) != "text" {
		t.Errorf("Read(/scratch/notes) = %q, want %q", content, "text")
	}

	// The dispatcher forwards the accessor sqlfs answers and not the two it
	// refuses, which is the whole reason the accessors sit on the base
	// interface.
	if cut := table.AsTruncater(); cut == nil {
		t.Error("AsTruncater() through the table = nil, want the dispatcher")
	}
	if stream := table.AsStreamer(); stream != nil {
		t.Error("AsStreamer() through the table = a streamer, want nil; sqlfs refuses it")
	}
}
