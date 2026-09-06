package vfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// The rule is spelled once so that a name refused where it is written is
// refused everywhere it is read back.
func TestValidSegment(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{name: "main"},
		{name: "release-2"},
		{name: "a.snapshot_1"},
		{name: "...."},
		{name: "", want: fs.ErrInvalid},
		{name: "refs/heads/main", want: fs.ErrInvalid},
		{name: "/", want: fs.ErrInvalid},
		{name: ".", want: fs.ErrInvalid},
		{name: "..", want: fs.ErrInvalid},
	} {
		err := vfs.ValidSegment("ref name", tc.name)
		if !errors.Is(err, tc.want) {
			t.Errorf("ValidSegment(%q) = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// The kind is what the caller calls the thing, so the error names it rather
// than the rule.
func TestValidSegmentNamesTheKind(t *testing.T) {
	for _, name := range []string{"", "a/b", "."} {
		err := vfs.ValidSegment("workspace", name)
		if err == nil || !strings.Contains(err.Error(), "workspace") {
			t.Errorf("ValidSegment(workspace, %q) = %v, want an error naming the workspace", name, err)
		}
	}
}
