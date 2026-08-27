package streamfs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	under := prefix(name)

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
		fold(children, rest, stream.info(rest))
	}

	entries := slices.Collect(maps.Values(children))
	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// fold adds one stream to what ReadDir answers. The stream either names a child
// outright, or names one segment of a prefix that exists because it and others
// are under it.
//
// A stream and a prefix can share a name, and the stream is the one that wins:
// it describes itself, where a prefix is only ever described by what is beneath
// it.
func fold(children map[string]vfs.FileInfo, rest string, one vfs.FileInfo) {
	base, _, deeper := strings.Cut(rest, "/")
	if !deeper {
		one.Name = base
		children[base] = one
		return
	}

	existing, listed := children[base]
	if listed && !existing.IsDir() {
		return
	}
	if one.ModTime.After(existing.ModTime) {
		existing = vfs.FileInfo{Name: base, Mode: dirMode, ModTime: one.ModTime}
	}
	children[base] = existing
}
