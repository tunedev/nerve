package streamrotatefs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Mkdir has nothing to make. A log name is a name and not a path through
// directories, so there is no directory to hold one. It does not fail: a caller
// making a parent before writing under it is doing nothing, not doing wrong.
func (f *FS) Mkdir(_ context.Context, _ string) error { return nil }

// Stat describes name without reading it. It answers for logs and segments and
// never for the prefixes above them: a prefix exists only because entries are
// under it, so there is nothing at one to describe.
func (f *FS) Stat(_ context.Context, name string) (vfs.FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	held, err := f.find(name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %w", err)
	}
	return held.info(name), nil
}

// ReadDir lists what is directly under the prefix name, sorted by name. This is
// where rotation is seen: a segment appears here as it is sealed and leaves as
// retention drops it, so what a log has kept is a listing rather than something
// a caller has to be told.
//
// It does not refuse a name nothing is under, because a name is a prefix:
// listing one that holds nothing is how a caller finds out that it does.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	under := keyspace.Prefix(name)

	f.mu.RLock()
	children := make(map[string]vfs.FileInfo, len(f.logs))
	for at, held := range f.logs {
		if strings.HasPrefix(at, under) {
			rest := strings.TrimPrefix(at, under)
			keyspace.Fold(children, rest, held.info(rest))
		}
	}
	f.mu.RUnlock()

	entries := slices.Collect(maps.Values(children))
	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}
