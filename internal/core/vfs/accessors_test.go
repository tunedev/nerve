package vfs_test

import (
	"context"
	"io"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// plain is a plugin that supports nothing optional: the ten verbs and its
// descriptors from stub, an embedded NoAccessors, and not one line of accessor
// code.
type plain struct {
	stub
	vfs.NoAccessors
}

var _ vfs.FileSystem = plain{}

func TestPluginSupportingNothingOptionalNeedsNoAccessorCode(t *testing.T) {
	var fsys vfs.FileSystem = plain{}

	if s := fsys.AsSymlinker(); s != nil {
		t.Errorf("AsSymlinker() = %v, want nil", s)
	}
	if s := fsys.AsStreamer(); s != nil {
		t.Errorf("AsStreamer() = %v, want nil", s)
	}
	if cut := fsys.AsTruncater(); cut != nil {
		t.Errorf("AsTruncater() = %v, want nil", cut)
	}
}

// symlinking is a plugin that supports one optional verb set. It overrides the
// one accessor it answers and inherits the refusal of the other two.
type symlinking struct {
	plain
}

func (s symlinking) AsSymlinker() vfs.Symlinker { return s }

func (symlinking) Symlink(context.Context, string, string) error { return nil }
func (symlinking) Readlink(context.Context, string) (string, error) {
	return "/elsewhere", nil
}
func (symlinking) Lstat(context.Context, string) (vfs.FileInfo, error) {
	return vfs.FileInfo{Name: "link", Mode: fs.ModeSymlink | 0o777}, nil
}

func TestOverridingOneAccessorLeavesTheOthersRefused(t *testing.T) {
	var fsys vfs.FileSystem = symlinking{}

	link := fsys.AsSymlinker()
	if link == nil {
		t.Fatal("AsSymlinker() = nil, want the plugin")
	}
	target, err := link.Readlink(context.Background(), "/anywhere")
	if err != nil || target != "/elsewhere" {
		t.Errorf("Readlink() = %q, %v, want %q, nil", target, err, "/elsewhere")
	}

	if s := fsys.AsStreamer(); s != nil {
		t.Errorf("AsStreamer() = %v, want nil", s)
	}
}

// Each narrow interface names a verb set a plugin may refuse wholesale, so each
// must be satisfiable on its own.
var (
	_ vfs.Streamer  = streaming{}
	_ vfs.Truncater = truncating{}
)

type streaming struct{}

func (streaming) OpenRead(context.Context, string) (io.ReadCloser, error)   { return nil, nil }
func (streaming) OpenWrite(context.Context, string) (io.WriteCloser, error) { return nil, nil }

type truncating struct{}

func (truncating) Truncate(context.Context, string, int64) error { return nil }

// describing is a plugin with no links whose Stat names the entry. It is what
// Lstat has to fall through to: a kind with no links cannot have one at a path,
// so its Stat already describes what stands there.
type describing struct {
	plain
}

func (describing) Stat(_ context.Context, path string) (vfs.FileInfo, error) {
	return vfs.FileInfo{Name: path, Mode: 0o644}, nil
}

func TestLstatOfAKindWithNoLinksIsItsStat(t *testing.T) {
	found, err := vfs.Lstat(context.Background(), describing{}, "/notes")
	if err != nil {
		t.Fatalf("Lstat() = %v, want nil", err)
	}
	if found.Name != "/notes" || found.IsSymlink() {
		t.Errorf("Lstat() = %+v, want the plugin's Stat of the same path", found)
	}
}

func TestLstatOfAKindWithLinksReachesTheLinkVerbs(t *testing.T) {
	found, err := vfs.Lstat(context.Background(), symlinking{}, "/peek")
	if err != nil {
		t.Fatalf("Lstat() = %v, want nil", err)
	}
	if !found.IsSymlink() {
		t.Errorf("Lstat() = %+v, want the link the plugin's own Lstat describes", found)
	}
}
