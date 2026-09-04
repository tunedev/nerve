package vectorfs

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a similarity index.
const Kind = "vectorfs"

// The settings one mount takes. The URL carries credentials; the rest do not.
const (
	settingName       = "name"
	settingIndex      = "index_url"
	settingDimension  = "dimension"
	settingNeighbours = "neighbours"
)

// DefaultNeighbours is how many documents a question is answered with when a
// mount does not say. It is a context window's worth rather than a database's:
// the answer is read by something with room for a handful of documents.
const DefaultNeighbours = 10

// Public names the settings safe to read back to an operator. The URL is not
// among them, so `GET /config` shows that a mount carries one without showing
// what it is.
var Public = []string{settingName, settingDimension, settingNeighbours}

// known is every setting this kind accepts, which is what makes a misspelling a
// boot failure instead of a silently ignored line.
var known = []string{settingName, settingIndex, settingDimension, settingNeighbours}

// required is what an index cannot be built without. There is no guess at where
// the corpus is, what to call it, or how wide the model's vectors are: the
// column is declared from that number and nothing can infer it.
var required = []string{settingName, settingIndex, settingDimension}

// Factory builds indexes that embed through one chain, which is what keeps a
// model client out of the plugin: the composition root owns the gateway, and
// every mount from this factory asks it.
//
// A build with no embedding endpoint behind the chain has a nil Embedder, and
// an index over one could store nothing and answer nothing. That is refused
// here, where an operator is still watching, rather than on the first document.
func Factory(embed llm.Embedder) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		if embed == nil {
			return nil, fmt.Errorf("kind %q needs an embedding endpoint, and no provider in the chain embeds: %w",
				Kind, fs.ErrInvalid)
		}
		if unknown := unknownSettings(settings); len(unknown) > 0 {
			return nil, fmt.Errorf("kind %q does not take %s; it takes %s: %w",
				Kind, strings.Join(unknown, ", "), strings.Join(known, ", "), fs.ErrInvalid)
		}
		if missing := missingSettings(settings); len(missing) > 0 {
			return nil, fmt.Errorf("kind %q needs %s: %w", Kind, strings.Join(missing, ", "), fs.ErrInvalid)
		}

		dimension, err := countSetting(settingDimension, settings[settingDimension], 0)
		if err != nil {
			return nil, err
		}
		neighbours, err := countSetting(settingNeighbours, settings[settingNeighbours], DefaultNeighbours)
		if err != nil {
			return nil, err
		}

		name := settings[settingName]
		index, err := OpenPostgres(settings[settingIndex], name, dimension)
		if err != nil {
			return nil, fmt.Errorf("kind %q: %w", Kind, err)
		}
		return holding(New(name, dimension, neighbours, index, embed), index), nil
	}
}

// countSetting reads a setting that is a count, falling back to its default
// when nothing was said. A spelling that is not a count is refused rather than
// defaulted: a mount whose dimension was mistyped would size a column from a
// number nobody chose.
func countSetting(name, spelled string, fallback int) (int, error) {
	if spelled == "" {
		return fallback, nil
	}

	count, err := strconv.Atoi(spelled)
	if err != nil || count <= 0 {
		return 0, fmt.Errorf("kind %q setting %q = %q, which is not a count: %w",
			Kind, name, spelled, fs.ErrInvalid)
	}
	return count, nil
}

func unknownSettings(settings map[string]string) []string {
	var unknown []string
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		if !slices.Contains(known, name) {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

func missingSettings(settings map[string]string) []string {
	var missing []string
	for _, name := range required {
		if settings[name] == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
