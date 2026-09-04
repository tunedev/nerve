package vectorfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name.
//
// Under the corpus a name is a document or it is a prefix, and the two are
// answered differently: a document is what the index says it is, and a prefix
// is a directory only for as long as a document sits under it.
//
// A question has no stored existence, so describing one costs what answering it
// costs: the size reported is the size of the answer, and getting it means
// asking. A caller that wants the answer should read rather than stat first.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	spot, rest := locate(name)
	switch spot {
	case atRoot:
		return dirInfo(separator), nil
	case atCorpus:
		return dirInfo(Docs), nil
	case atQuery:
		return dirInfo(Query), nil
	case atConfig:
		return vfs.FileInfo{Name: Config, Size: int64(len(f.description())), Mode: fileMode}, nil
	case atDoc:
		return f.statDoc(ctx, name, rest)
	case atQuestion:
		answer, err := f.answer(ctx, rest)
		if err != nil {
			return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
		}
		return vfs.FileInfo{Name: base(rest), Size: int64(len(answer)), Mode: fileMode}, nil
	default:
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
}

// ReadDir lists what sits directly under name, sorted by name. A prefix with
// nothing under it lists nothing rather than failing: there was no directory to
// be missing.
//
// The questions list nothing, ever. Every name under them answers, so there is
// nothing to enumerate: a listing would either be empty or be the set of
// questions somebody happened to ask, and only the first of those is true.
//
// A document and a prefix can share a name, which no tree allows, and the
// document is the one listed: it describes itself, where a prefix is only ever
// described by what sits beneath it. Both stay reachable by name; only the
// listing has to choose.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	spot, key := locate(name)
	switch spot {
	case atRoot:
		return []vfs.FileInfo{
			{Name: Config, Size: int64(len(f.description())), Mode: fileMode},
			dirInfo(Docs),
			dirInfo(Query),
		}, nil
	case atQuery:
		return nil, nil
	case atCorpus, atDoc:
		return f.listCorpus(ctx, name, prefixOf(key))
	default:
		return nil, fmt.Errorf("read_dir %q: %w", name, fs.ErrNotExist)
	}
}

// statDoc answers for a name under the corpus: the document stored under it,
// the directory a prefix makes while something is beneath it, or neither.
func (f *FS) statDoc(ctx context.Context, name, key string) (vfs.FileInfo, error) {
	entry, err := f.index.Peek(ctx, key)
	if err == nil {
		return entry.info(), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}

	beneath, err := f.index.List(ctx, prefixOf(key))
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	if len(beneath) == 0 {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
	return dirInfo(key), nil
}

// listCorpus folds the keys under a prefix into one level of a tree: a key with
// no separator left in it is a document, and everything a separator cuts short
// is one directory however many keys are under it.
func (f *FS) listCorpus(ctx context.Context, name, prefix string) ([]vfs.FileInfo, error) {
	beneath, err := f.index.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, err)
	}

	entries := make([]vfs.FileInfo, 0, len(beneath))
	named := make(map[string]struct{}, len(beneath))
	for _, entry := range beneath {
		rest := strings.TrimPrefix(entry.Key, prefix)
		if strings.Contains(rest, separator) {
			continue
		}
		named[rest] = struct{}{}
		entries = append(entries, entry.info())
	}
	for _, entry := range beneath {
		rest := strings.TrimPrefix(entry.Key, prefix)
		cut := strings.Index(rest, separator)
		if cut < 0 {
			continue
		}
		if _, taken := named[rest[:cut]]; taken {
			continue
		}
		named[rest[:cut]] = struct{}{}
		entries = append(entries, dirInfo(rest[:cut]))
	}

	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// prefixOf is the key a listing walks to find what sits under one. The corpus
// itself lists everything, so its prefix is empty rather than a bare separator.
func prefixOf(key string) string {
	if key == "" {
		return ""
	}
	return key + separator
}
