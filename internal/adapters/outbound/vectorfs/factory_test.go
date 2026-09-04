package vectorfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// A mount is refused for what it says before anything is dialled, so a config
// that could never work is a boot failure rather than a mount that looks
// healthy and answers nothing.
func TestFactoryRefusesSettingsBeforeReachingTheDatabase(t *testing.T) {
	build := vectorfs.Factory(bagEmbedder{width: width})

	refusals := map[string]map[string]string{
		"no name":            {"index_url": "postgres://nowhere", "dimension": "8"},
		"no database":        {"name": "memories", "dimension": "8"},
		"no dimension":       {"name": "memories", "index_url": "postgres://nowhere"},
		"a misspelt setting": {"name": "memories", "index_url": "postgres://nowhere", "dimension": "8", "dimensions": "8"},
		"a dimension that is not a count": {
			"name": "memories", "index_url": "postgres://nowhere", "dimension": "wide",
		},
		"neighbours that are not a count": {
			"name": "memories", "index_url": "postgres://nowhere", "dimension": "8", "neighbours": "-1",
		},
	}
	for what, settings := range refusals {
		if _, err := build(settings); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("a mount with %s = %v, want fs.ErrInvalid", what, err)
		}
	}
}

// The index's name becomes a table name in DDL that no parameter can carry, so
// a name that is not a bare word is refused rather than escaped.
func TestFactoryRefusesANameATableCannotBeCalled(t *testing.T) {
	build := vectorfs.Factory(bagEmbedder{width: width})

	for _, name := range []string{"Memories", "memories; drop table x", "9lives", "", strings.Repeat("m", 64)} {
		_, err := build(map[string]string{
			"name": name, "index_url": "postgres://nowhere", "dimension": "8",
		})
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("a mount named %q = %v, want fs.ErrInvalid", name, err)
		}
	}
}
