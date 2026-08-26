package vfs_test

import (
	"context"
	"io/fs"
	"reflect"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// stub is the whole cost of satisfying the port: ten methods and nothing else.
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

var _ vfs.FileSystem = stub{}

// The verb set is fixed. A new capability arrives as a descriptor or an
// accessor, never as an eleventh method, because every added verb is one more
// thing every plugin has to answer.
func TestVerbSetIsTheTenVerbs(t *testing.T) {
	want := []string{
		"Chmod", "Create", "Mkdir", "Read", "ReadDir",
		"Remove", "RemoveAll", "Rename", "Stat", "Write",
	}

	iface := reflect.TypeOf((*vfs.FileSystem)(nil)).Elem()
	got := make([]string, iface.NumMethod())
	for i := range got {
		got[i] = iface.Method(i).Name
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("FileSystem verbs = %v, want %v", got, want)
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
