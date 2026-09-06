package vfs_test

import (
	"context"
	"io/fs"
	"reflect"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// stub writes the ten verbs and the descriptor set, and nothing else. It is not
// a FileSystem on its own; embedding NoAccessors alongside it is what completes
// the port.
type stub struct{}

func (stub) Create(context.Context, string) error                    { return nil }
func (stub) Mkdir(context.Context, string) error                     { return nil }
func (stub) Remove(context.Context, string) error                    { return nil }
func (stub) RemoveAll(context.Context, string) error                 { return nil }
func (stub) Read(context.Context, string) ([]byte, error)            { return nil, nil }
func (stub) Write(context.Context, string, []byte) error             { return nil }
func (stub) ReadDir(context.Context, string) ([]vfs.FileInfo, error) { return nil, nil }
func (stub) Stat(context.Context, string) (vfs.FileInfo, error)      { return vfs.FileInfo{}, nil }
func (stub) Rename(context.Context, string, string) error            { return nil }
func (stub) Chmod(context.Context, string, fs.FileMode) error        { return nil }
func (stub) Capabilities(string) vfs.Caps                            { return vfs.Caps{} }

// The verb set is fixed at ten. A new capability arrives as a descriptor or an
// accessor, never as an eleventh verb, because every added verb is one more
// thing every plugin has to answer. The interface is therefore the ten verbs
// plus the entry points to that discovery, and nothing else.
func TestInterfaceIsTenVerbsPlusDiscovery(t *testing.T) {
	verbs := []string{
		"Chmod", "Create", "Mkdir", "Read", "ReadDir",
		"Remove", "RemoveAll", "Rename", "Stat", "Write",
	}
	discovery := []string{"Capabilities", "AsSymlinker", "AsStreamer", "AsTruncater", "AsSubber"}

	want := slices.Sorted(slices.Values(slices.Concat(verbs, discovery)))

	iface := reflect.TypeOf((*vfs.FileSystem)(nil)).Elem()
	got := make([]string, iface.NumMethod())
	for i := range got {
		got[i] = iface.Method(i).Name
	}

	if !slices.Equal(got, want) {
		t.Errorf("FileSystem methods = %v, want %v", got, want)
	}
}

func TestFileInfoIsDirFollowsMode(t *testing.T) {
	dir := vfs.FileInfo{Name: "jobs", Mode: fs.ModeDir | 0o755}
	if !dir.IsDir() {
		t.Error("entry with ModeDir set reports IsDir() = false")
	}

	file := vfs.FileInfo{Name: "jobs", Mode: 0o644}
	if file.IsDir() {
		t.Error("entry without ModeDir set reports IsDir() = true")
	}
}
