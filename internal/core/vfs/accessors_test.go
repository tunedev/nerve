package vfs_test

import (
	"context"
	"io"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// plain is a plugin that supports nothing optional: the ten verbs, an embedded
// NoCaps, and not one line of accessor code.
type plain struct {
	stub
	vfs.NoCaps
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
