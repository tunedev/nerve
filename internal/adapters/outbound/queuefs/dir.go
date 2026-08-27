package queuefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without consuming anything. Reading a message is what
// takes it out of the queue, so this is how a caller looks at one and leaves it
// where it is.
//
// Under the queue a name is a message or it is a prefix, and the two are
// answered differently: a message is what the journal says it is, and a prefix
// is a directory only for as long as a message sits under it.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	spot, key := locate(name)
	switch spot {
	case atRoot:
		return dirInfo(separator), nil
	case atQueue:
		return dirInfo(Jobs), nil
	case atConfig:
		return vfs.FileInfo{Name: Config, Size: int64(len(f.description())), Mode: fileMode}, nil
	case atMessage:
		return f.statMessage(ctx, name, key)
	default:
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
}

// ReadDir lists what sits directly under name, sorted by name. A prefix with
// nothing under it lists nothing rather than failing: there was no directory to
// be missing.
//
// A message and a prefix can share a name, which no tree allows, and the
// message is the one listed: it describes itself, where a prefix is only ever
// described by what sits beneath it. Both stay reachable by name; only the
// listing has to choose.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	spot, key := locate(name)
	switch spot {
	case atRoot:
		return []vfs.FileInfo{
			{Name: Config, Size: int64(len(f.description())), Mode: fileMode},
			dirInfo(Jobs),
		}, nil
	case atQueue, atMessage:
		return f.listQueue(ctx, name, prefixOf(key))
	default:
		return nil, fmt.Errorf("read_dir %q: %w", name, fs.ErrNotExist)
	}
}

// statMessage answers for a name under the queue: the message queued under it,
// the directory a prefix makes while something is beneath it, or neither.
func (f *FS) statMessage(ctx context.Context, name, key string) (vfs.FileInfo, error) {
	entry, err := f.journal.Peek(ctx, key)
	if err == nil {
		return entry.info(), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}

	beneath, err := f.journal.List(ctx, prefixOf(key))
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	if len(beneath) == 0 {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
	return dirInfo(key), nil
}

// listQueue folds the keys under a prefix into one level of a tree: a key with
// no separator left in it is a message, and everything a separator cuts short
// is one directory however many keys are under it.
func (f *FS) listQueue(ctx context.Context, name, prefix string) ([]vfs.FileInfo, error) {
	beneath, err := f.journal.List(ctx, prefix)
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

// prefixOf is the key a listing walks to find what sits under one. The queue
// itself lists everything, so its prefix is empty rather than a bare separator.
func prefixOf(key string) string {
	if key == "" {
		return ""
	}
	return key + separator
}
