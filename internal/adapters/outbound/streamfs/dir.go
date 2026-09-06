package streamfs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Mkdir has nothing to make. A stream name is a name and not a path through
// directories, so there is no directory to hold one. It does not fail: a caller
// making a parent before writing under it is doing nothing, not doing wrong.
func (f *FS) Mkdir(_ context.Context, _ string) error { return nil }

// Stat describes name without reading it. It answers for streams and never for
// the prefixes above them: a prefix exists only because streams are under it,
// so there is nothing at one to describe.
func (f *FS) Stat(_ context.Context, name string) (vfs.FileInfo, error) {
	stream, err := f.find(name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %w", err)
	}
	return stream.info(name), nil
}

// ReadDir lists what is directly under the prefix name, sorted by name. It does
// not refuse a name nothing is under, because a name is a prefix: listing one
// that holds nothing is how a caller finds out that it does.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	under := keyspace.Prefix(name)

	f.mu.RLock()
	beneath := make(map[string]*ring, len(f.streams))
	for at, stream := range f.streams {
		if strings.HasPrefix(at, under) {
			beneath[strings.TrimPrefix(at, under)] = stream
		}
	}
	f.mu.RUnlock()

	children := make(map[string]vfs.FileInfo, len(beneath))
	for rest, stream := range beneath {
		keyspace.Fold(children, rest, stream.info(rest))
	}

	entries := slices.Collect(maps.Values(children))
	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}
