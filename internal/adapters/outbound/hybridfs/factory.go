package hybridfs

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Kind is the name a mount config uses to ask for a fusion.
const Kind = "hybridfs"

// The settings one mount takes. None carries a credential.
const (
	settingName    = "name"
	settingSources = "sources"
	settingDamping = "damping"
	settingResults = "results"
)

// The sources setting is a list of rankings, each a name and the path in the
// tree it answers questions at:
//
//	vector=/index/query;graph=/graph/mentions
const (
	betweenSources = ";"
	atPath         = "="
)

// DefaultDamping is the constant a rank is damped by when a mount does not say.
// It is the value Reciprocal Rank Fusion was published with, and it is large
// enough that the whole of one ranker's top ten is worth less than a key two
// rankers both named.
const DefaultDamping = 60

// DefaultResults is how many keys a fused answer carries when a mount does not
// say. It is a context window's worth rather than an index's, for the reason an
// index's own bound is.
const DefaultResults = 10

// Public names the settings safe to read back to an operator, which is all of
// them: none is a credential, and which rankings a fusion reads is the first
// thing anyone asks when an answer looks wrong.
var Public = []string{settingName, settingSources, settingDamping, settingResults}

// known is every setting this kind accepts, which is what makes a misspelling a
// boot failure instead of a silently ignored line.
var known = []string{settingName, settingSources, settingDamping, settingResults}

// Factory builds fusions that read their rankings from one tree.
//
// The tree arrives as a function rather than a filesystem because a fusion
// mount is part of the tree it reads: every mount in the table is built before
// the table exists, and this one is one of them. Nothing calls the function
// until a question is asked, by which time the table is there.
//
// A build with no tree behind it hands over a nil, and the kind refuses a mount
// rather than booting a fusion that could answer nothing.
//
// Where a source's ranking comes from is a path and not a kind, which is what
// makes fusing a new sort of index a line of config: anything mounted anywhere
// that answers a question with a ranking is a source, and this package knows
// none of their names.
func Factory(tree func() vfs.FileSystem) vfs.Factory {
	return func(settings map[string]string) (vfs.FileSystem, error) {
		if tree == nil {
			return nil, fmt.Errorf("kind %q needs a tree to read its rankings from, and this build names none: %w",
				Kind, fs.ErrInvalid)
		}
		if unknown := unknownSettings(settings); len(unknown) > 0 {
			return nil, fmt.Errorf("kind %q does not take %s; it takes %s: %w",
				Kind, strings.Join(unknown, ", "), strings.Join(known, ", "), fs.ErrInvalid)
		}
		if settings[settingName] == "" {
			return nil, fmt.Errorf("kind %q needs %s: %w", Kind, settingName, fs.ErrInvalid)
		}

		sources, err := parseSources(settings[settingSources])
		if err != nil {
			return nil, err
		}
		damping, err := countSetting(settingDamping, settings[settingDamping], DefaultDamping)
		if err != nil {
			return nil, err
		}
		results, err := countSetting(settingResults, settings[settingResults], DefaultResults)
		if err != nil {
			return nil, err
		}

		return New(settings[settingName], damping, results, sources, tree), nil
	}
}

// parseSources reads the rankings a config named. Each is a name to report the
// ranking under and the path it is read at. Whitespace around either is an
// operator's spacing rather than part of a name.
//
// One source is refused. A fusion of one ranking is that ranking with its
// scores replaced by worse ones, and an operator who wrote one meant to write
// two: saying so at boot is cheaper than an answer that looks fused and is not.
func parseSources(declared string) ([]Source, error) {
	var sources []Source
	named := map[string]struct{}{}

	for _, one := range strings.Split(declared, betweenSources) {
		if strings.TrimSpace(one) == "" {
			continue
		}

		name, where, found := strings.Cut(one, atPath)
		name, where = strings.TrimSpace(name), strings.TrimSpace(where)
		if !found || name == "" || where == "" {
			return nil, fmt.Errorf("kind %q source %q names no ranking, spelled %s: %w",
				Kind, strings.TrimSpace(one), "name=/path;name=/path", fs.ErrInvalid)
		}
		if _, already := named[name]; already {
			return nil, fmt.Errorf("kind %q names the source %q twice, and a ranker that voted twice would outvote the rest: %w",
				Kind, name, fs.ErrInvalid)
		}

		named[name] = struct{}{}
		sources = append(sources, Source{Name: name, Path: path.Join(separator, where)})
	}

	if len(sources) < 2 {
		return nil, fmt.Errorf("kind %q fuses two or more rankings and was given %d, spelled %s: %w",
			Kind, len(sources), "name=/path;name=/path", fs.ErrInvalid)
	}
	return sources, nil
}

// countSetting reads a setting that is a count, falling back to its default
// when nothing was said. A spelling that is not a count is refused rather than
// defaulted: a mount whose damping was mistyped would fuse with a constant
// nobody chose.
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
