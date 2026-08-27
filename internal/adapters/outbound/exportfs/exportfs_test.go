package exportfs_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/exportfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// counted wraps a materialisation with a tally of how often it ran, which is
// how a test tells a held export from one made again.
type counted struct {
	runs atomic.Int64
	from exportfs.Materialise
}

func (c *counted) materialise(ctx context.Context, source vfs.FileSystem, inputs []string) ([]byte, error) {
	c.runs.Add(1)
	return c.from(ctx, source, inputs)
}

// source returns a memfs holding the named content, which is what an export in
// these tests is derived from.
func source(t *testing.T, content map[string]string) vfs.FileSystem {
	t.Helper()

	fsys := memfs.New()
	for name, data := range content {
		if err := fsys.Write(t.Context(), name, []byte(data)); err != nil {
			t.Fatalf("Write(%s) = %v", name, err)
		}
	}
	return fsys
}

// mount returns an export mount over src, holding one export named "report"
// derived from inputs, together with the tally of its materialisations.
func mount(t *testing.T, src vfs.FileSystem, inputs ...string) (vfs.FileSystem, *counted) {
	t.Helper()

	made := &counted{from: exportfs.Join}
	fsys, err := exportfs.New(src, []exportfs.Export{{
		Name:   "report",
		Inputs: inputs,
		From:   made.materialise,
	}})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return fsys, made
}

func read(t *testing.T, fsys vfs.FileSystem, name string) string {
	t.Helper()

	content, err := fsys.Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%s) = %v", name, err)
	}
	return string(content)
}

func TestAStaleExportIsNotServedAfterAnInputChanges(t *testing.T) {
	src := source(t, map[string]string{"/a": "one", "/b": "two"})
	fsys, _ := mount(t, src, "/a", "/b")

	if held := read(t, fsys, "/report"); held != "onetwo" {
		t.Fatalf("the export as first materialised = %q, want %q", held, "onetwo")
	}

	if err := src.Write(t.Context(), "/b", []byte("three")); err != nil {
		t.Fatalf("Write(/b) = %v", err)
	}

	if held := read(t, fsys, "/report"); held != "onethree" {
		t.Errorf("the export after an input changed = %q, want %q", held, "onethree")
	}
}

func TestAnInputChangedWithoutChangingLengthStillRefreshes(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	fsys, _ := mount(t, src, "/a")

	read(t, fsys, "/report")

	if err := src.Write(t.Context(), "/a", []byte("two")); err != nil {
		t.Fatalf("Write(/a) = %v", err)
	}

	if held := read(t, fsys, "/report"); held != "two" {
		t.Errorf("the export after an input of the same length changed = %q, want %q", held, "two")
	}
}

func TestAnExportIsMaterialisedOnceWhileItsInputsHold(t *testing.T) {
	fsys, made := mount(t, source(t, map[string]string{"/a": "one"}), "/a")

	for range 3 {
		read(t, fsys, "/report")
	}

	if runs := made.runs.Load(); runs != 1 {
		t.Errorf("materialisations over three reads of an unchanged export = %d, want 1", runs)
	}
}

func TestAnExportIsMaterialisedAgainOnlyAfterAnInputChanges(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	fsys, made := mount(t, src, "/a")

	read(t, fsys, "/report")
	if err := src.Write(t.Context(), "/a", []byte("changed")); err != nil {
		t.Fatalf("Write(/a) = %v", err)
	}
	read(t, fsys, "/report")
	read(t, fsys, "/report")

	if runs := made.runs.Load(); runs != 2 {
		t.Errorf("materialisations over one change and three reads = %d, want 2", runs)
	}
}

func TestAnExportRefreshesAfterAnInputIsRemoved(t *testing.T) {
	src := source(t, map[string]string{"/a": "one", "/b": "two"})
	fsys, _ := mount(t, src, "/a", "/b")

	read(t, fsys, "/report")

	if err := src.Remove(t.Context(), "/b"); err != nil {
		t.Fatalf("Remove(/b) = %v", err)
	}

	_, err := fsys.Read(t.Context(), "/report")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/report) after an input was removed = %v, want %v", err, fs.ErrNotExist)
	}
}

func TestAnExportRefreshesAfterAnAbsentInputAppears(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	fsys, _ := mount(t, src, "/a", "/b")

	if _, err := fsys.Read(t.Context(), "/report"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read(/report) with an input missing = %v, want %v", err, fs.ErrNotExist)
	}

	if err := src.Write(t.Context(), "/b", []byte("two")); err != nil {
		t.Fatalf("Write(/b) = %v", err)
	}

	if held := read(t, fsys, "/report"); held != "onetwo" {
		t.Errorf("the export once every input existed = %q, want %q", held, "onetwo")
	}
}

func TestStatDoesNotDescribeAStaleExport(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	fsys, _ := mount(t, src, "/a")

	before, err := fsys.Stat(t.Context(), "/report")
	if err != nil {
		t.Fatalf("Stat(/report) = %v", err)
	}

	if err := src.Write(t.Context(), "/a", []byte("a longer one")); err != nil {
		t.Fatalf("Write(/a) = %v", err)
	}

	after, err := fsys.Stat(t.Context(), "/report")
	if err != nil {
		t.Fatalf("Stat(/report) = %v", err)
	}
	if after.Size != int64(len("a longer one")) {
		t.Errorf("Stat(/report).Size after an input changed = %d, want %d", after.Size, len("a longer one"))
	}
	if !after.ModTime.After(before.ModTime) {
		t.Errorf("Stat(/report).ModTime after an input changed = %v, want later than %v", after.ModTime, before.ModTime)
	}
}

func TestReadDirDoesNotListAStaleExport(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	fsys, _ := mount(t, src, "/a")

	read(t, fsys, "/report")
	if err := src.Write(t.Context(), "/a", []byte("a longer one")); err != nil {
		t.Fatalf("Write(/a) = %v", err)
	}

	listed, err := fsys.ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ReadDir(/) listed %d entries, want 1", len(listed))
	}
	if listed[0].Size != int64(len("a longer one")) {
		t.Errorf("ReadDir(/)[0].Size after an input changed = %d, want %d", listed[0].Size, len("a longer one"))
	}
}

func TestAnExportRefusesToBeWritten(t *testing.T) {
	fsys, _ := mount(t, source(t, map[string]string{"/a": "one"}), "/a")

	err := fsys.Write(t.Context(), "/report", []byte("by hand"))
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Write(/report) = %v, want %v", err, fs.ErrPermission)
	}
}

func TestJoinPutsInputsInTheOrderTheExportNamedThem(t *testing.T) {
	src := source(t, map[string]string{"/a": "one", "/b": "two"})

	joined, err := exportfs.Join(t.Context(), src, []string{"/b", "/a"})
	if err != nil {
		t.Fatalf("Join() = %v", err)
	}
	if string(joined) != "twoone" {
		t.Errorf("Join(/b, /a) = %q, want %q", joined, "twoone")
	}
}

func TestNewRefusesAnExportItCannotAnswerFor(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	one := exportfs.Export{Name: "report", Inputs: []string{"/a"}, From: exportfs.Join}

	for _, tc := range []struct {
		name    string
		exports []exportfs.Export
		want    error
	}{
		{"no exports at all", nil, fs.ErrInvalid},
		{"an export with no name", []exportfs.Export{{Inputs: []string{"/a"}, From: exportfs.Join}}, fs.ErrInvalid},
		{"a name that is a path", []exportfs.Export{{Name: "a/b", Inputs: []string{"/a"}, From: exportfs.Join}}, fs.ErrInvalid},
		{"an export derived from nothing", []exportfs.Export{{Name: "report", From: exportfs.Join}}, fs.ErrInvalid},
		{"an export with nothing that makes it", []exportfs.Export{{Name: "report", Inputs: []string{"/a"}}}, fs.ErrInvalid},
		{"the same name twice", []exportfs.Export{one, one}, fs.ErrExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := exportfs.New(src, tc.exports)
			if !errors.Is(err, tc.want) {
				t.Errorf("New() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAFailedMaterialisationIsNotHeld(t *testing.T) {
	src := source(t, map[string]string{"/a": "one"})
	var fail atomic.Bool
	fail.Store(true)

	fsys, err := exportfs.New(src, []exportfs.Export{{
		Name:   "report",
		Inputs: []string{"/a"},
		From: func(ctx context.Context, source vfs.FileSystem, inputs []string) ([]byte, error) {
			if fail.Load() {
				return nil, errors.New("the query engine was down")
			}
			return exportfs.Join(ctx, source, inputs)
		},
	}})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	if _, err := fsys.Read(t.Context(), "/report"); err == nil {
		t.Fatal("Read(/report) with a failing materialisation = nil, want an error")
	}

	fail.Store(false)
	if held := read(t, fsys, "/report"); held != "one" {
		t.Errorf("the export once its materialisation worked = %q, want %q", held, "one")
	}
}

func TestAnUnknownNameIsNotAnExport(t *testing.T) {
	fsys, _ := mount(t, source(t, map[string]string{"/a": "one"}), "/a")

	_, err := fsys.Read(t.Context(), "/nothing")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(/nothing) = %v, want %v", err, fs.ErrNotExist)
	}
	if err != nil && !strings.Contains(err.Error(), "nothing") {
		t.Errorf("Read(/nothing) = %v, want the name in the message", err)
	}
}
