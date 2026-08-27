package version_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/core/version"
)

// The rule lives in the core so that a name refused by one ref store is refused
// by every other one, whatever it keeps refs in.
func TestValidateRefName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{name: "main"},
		{name: "release-2"},
		{name: "a.snapshot_1"},
		{name: "", want: fs.ErrInvalid},
		{name: "refs/heads/main", want: fs.ErrInvalid},
		{name: "..", want: fs.ErrInvalid},
		{name: ".", want: fs.ErrInvalid},
	} {
		err := version.ValidateRefName(tc.name)
		if !errors.Is(err, tc.want) {
			t.Errorf("ValidateRefName(%q) = %v, want %v", tc.name, err, tc.want)
		}
	}
}
