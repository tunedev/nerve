package graphfs

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a knowledge graph.
const Kind = "graphfs"

// The settings one mount takes. Neither carries a credential.
const (
	settingName  = "name"
	settingEdges = "edges"
)

// DefaultEdges is how much of an answer a mount hands back when it does not
// say: the claims an expansion carries, and the texts a mentions answer ranks.
// It is a context window's worth rather than a graph's: the answer is read by
// something with room for a handful of rows, and a well-connected entity has
// more of both than any of them can use.
const DefaultEdges = 20

// Public names the settings safe to read back to an operator, which is both of
// them: what a graph is called and how much of it one answer carries are an
// operator's business, and where the blob is kept is the store's.
var Public = []string{settingName, settingEdges}

// known is every setting this kind accepts, which is what makes a misspelling a
// boot failure instead of a silently ignored line.
var known = []string{settingName, settingEdges}

// Factory builds graphs kept in one store that are read by one chain, which is
// what keeps both a model client and a bucket out of the plugin: the
// composition root owns the gateway and the store, and every mount from this
// factory is a part of each.
//
// A build with no extracting endpoint behind the chain has a nil
// EntityExtractor, and a build with nowhere to keep a graph has a nil Store and
// a nil SnapshotStore. A graph over any of them could learn nothing, keep
// nothing or never be put back, so all three are refused here, where an
// operator is still watching, rather than on the first text.
//
// The graph is loaded here, at boot, and a store that cannot be read is a mount
// that does not come up. This is the opposite of the rule about model
// endpoints, and for a reason: a mount that came up with an empty graph because
// a bucket was briefly unreachable would overwrite the real one on its first
// write. An endpoint that is down costs an answer; a store that is down would
// cost the graph.
func Factory(extract llm.EntityExtractor, store Store, snapshots SnapshotStore) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		if extract == nil {
			return nil, fmt.Errorf("kind %q needs an endpoint that reads entities, and no provider in the chain does: %w",
				Kind, fs.ErrInvalid)
		}
		if store == nil {
			return nil, fmt.Errorf("kind %q needs somewhere to keep a graph, and this build names none: %w",
				Kind, fs.ErrInvalid)
		}
		if snapshots == nil {
			return nil, fmt.Errorf("kind %q needs somewhere to keep a graph's snapshots, and this build names none: %w",
				Kind, fs.ErrInvalid)
		}
		if unknown := unknownSettings(settings); len(unknown) > 0 {
			return nil, fmt.Errorf("kind %q does not take %s; it takes %s: %w",
				Kind, strings.Join(unknown, ", "), strings.Join(known, ", "), fs.ErrInvalid)
		}
		if settings[settingName] == "" {
			return nil, fmt.Errorf("kind %q needs %s: %w", Kind, settingName, fs.ErrInvalid)
		}

		edges, err := countSetting(settingEdges, settings[settingEdges], DefaultEdges)
		if err != nil {
			return nil, err
		}

		name := settings[settingName]
		graph, err := Open(context.Background(), name, edges, store, snapshots, extract)
		if err != nil {
			return nil, fmt.Errorf("kind %q: %w", Kind, err)
		}
		return graph, nil
	}
}

// countSetting reads a setting that is a count, falling back to its default
// when nothing was said. A spelling that is not a count is refused rather than
// defaulted: a mount whose bound was mistyped would answer with a number nobody
// chose.
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
