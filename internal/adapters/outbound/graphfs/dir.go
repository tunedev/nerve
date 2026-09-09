package graphfs

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name.
//
// Under the texts a name is a text or it is a prefix, and the two are answered
// differently: a text is what the graph says it is, and a prefix is a directory
// only for as long as a text sits under it.
//
// An expansion and a mentions answer have no stored existence, so the size
// reported for one is the size of the answer. Unlike a similarity query that
// costs a model call, answering is a walk of what this process already holds,
// so a caller that stats before reading pays a walk twice and no round trip at
// all.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	spot, rest := locate(name)
	switch spot {
	case atRoot:
		return dirInfo(separator), nil
	case atCorpus:
		return dirInfo(Docs), nil
	case atNeighbours:
		return dirInfo(Neighbours), nil
	case atMentions:
		return dirInfo(Mentions), nil
	case atSnapshots:
		return dirInfo(Snapshots), nil
	case atRestore:
		return vfs.FileInfo{Name: Restore, Size: int64(len(f.standing())), Mode: fileMode}, nil
	case atSnapshot:
		return f.statSnapshot(ctx, name, rest)
	case atConfig:
		return vfs.FileInfo{Name: Config, Size: int64(len(f.description())), Mode: fileMode}, nil
	case atDoc:
		return f.statDoc(name, rest)
	case atEntity:
		return vfs.FileInfo{Name: base(rest), Size: int64(len(f.expand(rest))), Mode: fileMode}, nil
	case atMentioned:
		return vfs.FileInfo{Name: base(rest), Size: int64(len(f.mentions(rest))), Mode: fileMode}, nil
	default:
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
}

// ReadDir lists what sits directly under name, sorted by name. A prefix with
// nothing under it lists nothing rather than failing: there was no directory to
// be missing.
//
// The snapshots list, unlike the expansions: a label exists because somebody
// took it, so there is a set to enumerate and it is the useful thing to read
// before restoring one.
//
// The expansions and the mentions list nothing, ever. Every name under them
// answers, so there is nothing to enumerate: a listing would either be empty or
// be the set of entities somebody happened to ask about, and only the first of
// those is true.
//
// A text and a prefix can share a name, which no tree allows, and the text is
// the one listed: it describes itself, where a prefix is only ever described by
// what sits beneath it. Both stay reachable by name; only the listing has to
// choose.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	spot, key := locate(name)
	switch spot {
	case atRoot:
		return []vfs.FileInfo{
			{Name: Config, Size: int64(len(f.description())), Mode: fileMode},
			dirInfo(Docs),
			dirInfo(Mentions),
			dirInfo(Neighbours),
			{Name: Restore, Size: int64(len(f.standing())), Mode: fileMode},
			dirInfo(Snapshots),
		}, nil
	case atNeighbours, atMentions:
		return nil, nil
	case atCorpus, atDoc:
		return f.listTexts(prefixOf(key)), nil
	case atSnapshots:
		return f.listSnapshots(ctx)
	default:
		return nil, fmt.Errorf("read_dir %q: %w", name, fs.ErrNotExist)
	}
}

// listSnapshots names every snapshot kept of this graph. A label is described
// by what the store knows without fetching it, which is its name: a size and an
// instant would each cost a fetch of the whole graph per label, and both read
// back from the label's own path.
func (f *FS) listSnapshots(ctx context.Context) ([]vfs.FileInfo, error) {
	labels, err := f.snapshots.Labels(ctx, f.name)
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", Snapshots, err)
	}

	entries := make([]vfs.FileInfo, 0, len(labels))
	for _, label := range labels {
		entries = append(entries, vfs.FileInfo{Name: label, Mode: fileMode})
	}
	return entries, nil
}

// statSnapshot describes one snapshot from what it holds: the instant it was
// taken, and the size of the description its own path reads back.
func (f *FS) statSnapshot(ctx context.Context, name, label string) (vfs.FileInfo, error) {
	kept, err := f.fetch(ctx, label)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	return vfs.FileInfo{Name: label, Size: int64(len(described(kept))), Mode: fileMode, ModTime: kept.Taken}, nil
}

// statDoc answers for a name under the texts: the text stored under it, the
// directory a prefix makes while something is beneath it, or neither.
func (f *FS) statDoc(name, key string) (vfs.FileInfo, error) {
	if held, there := f.held(key); there {
		return held.info(key), nil
	}
	if f.anythingUnder(prefixOf(key)) {
		return dirInfo(key), nil
	}
	return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
}

// anythingUnder reports whether any text is filed beneath a prefix, which is
// the whole of what makes a prefix a directory.
func (f *FS) anythingUnder(prefix string) bool {
	found := false
	f.read(func(documents map[string]document) {
		for key := range documents {
			if strings.HasPrefix(key, prefix) {
				found = true
				return
			}
		}
	})
	return found
}

// listTexts folds the keys under a prefix into one level of a tree: a key with
// no separator left in it is a text, and everything a separator cuts short is
// one directory however many keys are under it.
func (f *FS) listTexts(prefix string) []vfs.FileInfo {
	var entries []vfs.FileInfo
	named := map[string]struct{}{}

	f.read(func(documents map[string]document) {
		for key, held := range documents {
			rest, under := strings.CutPrefix(key, prefix)
			if !under || strings.Contains(rest, separator) {
				continue
			}
			named[rest] = struct{}{}
			entries = append(entries, held.info(key))
		}
		for key := range documents {
			rest, under := strings.CutPrefix(key, prefix)
			cut := strings.Index(rest, separator)
			if !under || cut < 0 {
				continue
			}
			if _, taken := named[rest[:cut]]; taken {
				continue
			}
			named[rest[:cut]] = struct{}{}
			entries = append(entries, dirInfo(rest[:cut]))
		}
	})

	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries
}

// prefixOf is the key a listing walks to find what sits under one. The texts
// directory itself lists everything, so its prefix is empty rather than a bare
// separator.
func prefixOf(key string) string {
	if key == "" {
		return ""
	}
	return key + separator
}
