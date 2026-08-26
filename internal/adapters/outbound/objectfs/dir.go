package objectfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Stat describes name without reading it.
//
// A name is an object or it is a prefix, and the two are answered differently:
// an object is what the bucket says it is, and a prefix is a directory only for
// as long as something is stored under it. A prefix with nothing under it is
// not there, which is the difference from a tree that Mkdir has no work to do
// in.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	if isRoot(name) {
		return dirInfo(name), nil
	}

	object, err := f.bucket.Head(ctx, key(name))
	if err == nil {
		return object.info(), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}

	occupied, err := f.occupied(ctx, name)
	if err != nil {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
	}
	if !occupied {
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
	return dirInfo(name), nil
}

// ReadDir lists what sits directly under name, sorted by name. A prefix with
// nothing under it lists nothing rather than failing: there was no directory to
// be missing.
func (f *FS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	listing, err := f.bucket.List(ctx, prefixOf(name), separator)
	if err != nil {
		return nil, fmt.Errorf("read_dir %q: %w", name, err)
	}

	entries := make([]vfs.FileInfo, 0, len(listing.Objects)+len(listing.Prefixes))
	for _, object := range listing.Objects {
		entries = append(entries, object.info())
	}
	for _, prefix := range listing.Prefixes {
		entries = append(entries, dirInfo(prefix))
	}

	slices.SortFunc(entries, func(a, b vfs.FileInfo) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// occupied reports whether anything is stored under name, which is what makes a
// prefix a directory. One entry settles it, so the listing stops at the first
// separator rather than walking the whole subtree.
func (f *FS) occupied(ctx context.Context, name string) (bool, error) {
	listing, err := f.bucket.List(ctx, prefixOf(name), separator)
	if err != nil {
		return false, err
	}
	return len(listing.Objects) > 0 || len(listing.Prefixes) > 0, nil
}
