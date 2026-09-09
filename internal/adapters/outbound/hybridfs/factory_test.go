package hybridfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/hybridfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// tree is the one a built fusion reads through in these cases.
func tree() func() vfs.FileSystem {
	held := memfs.New()
	return func() vfs.FileSystem { return held }
}

func TestFactoryBuildsAFusionFromItsSettings(t *testing.T) {
	mount, err := hybridfs.Factory(tree())(map[string]string{
		"name":    "retrieval",
		"sources": "vector=/index/query;graph=/graph/mentions",
		"damping": "30",
		"results": "3",
	})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	answer, err := mount.Read(t.Context(), "/config")
	if err != nil {
		t.Fatalf("Read(/config) = %v", err)
	}

	want := "hybrid retrieval\ndamping 30\nresults 3\nsources vector=/index/query;graph=/graph/mentions\n"
	if string(answer) != want {
		t.Errorf("Read(/config) =\n%q\nwant\n%q", answer, want)
	}
}

// A build with no tree behind it could read no ranking, so the refusal lands
// where an operator is still watching rather than on the first question.
func TestFactoryRefusesABuildWithNoTree(t *testing.T) {
	_, err := hybridfs.Factory(nil)(map[string]string{
		"name":    "retrieval",
		"sources": "vector=/index/query;graph=/graph/mentions",
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a fusion with no tree = %v, want fs.ErrInvalid", err)
	}
}

// A fusion of one ranking is that ranking with worse scores, and an operator
// who wrote one meant to write two.
func TestFactoryRefusesFewerThanTwoSources(t *testing.T) {
	for name, sources := range map[string]string{
		"none": "",
		"one":  "vector=/index/query",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := hybridfs.Factory(tree())(map[string]string{"name": "retrieval", "sources": sources})
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("a fusion over %s = %v, want fs.ErrInvalid", name, err)
			}
		})
	}
}

// A source named twice would be one ranker voting twice, which is exactly what
// rank fusion is for stopping.
func TestFactoryRefusesTheSameSourceNameTwice(t *testing.T) {
	_, err := hybridfs.Factory(tree())(map[string]string{
		"name":    "retrieval",
		"sources": "vector=/index/query;vector=/other/query",
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a fusion naming one source twice = %v, want fs.ErrInvalid", err)
	}
}

func TestFactoryRefusesASourceWithNoPath(t *testing.T) {
	_, err := hybridfs.Factory(tree())(map[string]string{
		"name":    "retrieval",
		"sources": "vector;graph=/graph/mentions",
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a source with no path = %v, want fs.ErrInvalid", err)
	}
}

func TestFactoryRefusesAMountWithNoName(t *testing.T) {
	_, err := hybridfs.Factory(tree())(map[string]string{
		"sources": "vector=/index/query;graph=/graph/mentions",
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a fusion with no name = %v, want fs.ErrInvalid", err)
	}
}

// A misspelled setting is a boot failure rather than a line nobody reads.
func TestFactoryRefusesASettingItDoesNotTake(t *testing.T) {
	_, err := hybridfs.Factory(tree())(map[string]string{
		"name":      "retrieval",
		"sources":   "vector=/index/query;graph=/graph/mentions",
		"dampening": "30",
	})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("a misspelled setting = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "damping") {
		t.Errorf("the refusal = %v, want it to name the settings the kind takes", err)
	}
}

// A count that is not one is refused rather than defaulted: a fusion whose
// damping was mistyped would fuse with a constant nobody chose.
func TestFactoryRefusesACountThatIsNotOne(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"damping": {"damping": "soon"},
		"results": {"results": "-1"},
	} {
		t.Run(name, func(t *testing.T) {
			settings["name"] = "retrieval"
			settings["sources"] = "vector=/index/query;graph=/graph/mentions"

			_, err := hybridfs.Factory(tree())(settings)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%s of %q = %v, want fs.ErrInvalid", name, settings[name], err)
			}
		})
	}
}

// Whitespace around a source is an operator's spacing rather than part of a
// name, and a path is rooted however it was spelled.
func TestFactoryReadsASourceSpelledWithSpacing(t *testing.T) {
	mount, err := hybridfs.Factory(tree())(map[string]string{
		"name":    "retrieval",
		"sources": " vector = index/query ; graph = /graph/mentions ",
	})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	answer, err := mount.Read(t.Context(), "/config")
	if err != nil {
		t.Fatalf("Read(/config) = %v", err)
	}
	if !strings.Contains(string(answer), "sources vector=/index/query;graph=/graph/mentions\n") {
		t.Errorf("Read(/config) =\n%s\nwant the sources trimmed and rooted", answer)
	}
}
