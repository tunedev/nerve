package vectorfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// LocalDocs is a corpus in a directory on the host disk: the local half of the
// substitution the platform is built around, and what a laptop with no bucket
// keeps its documents in.
//
// A key is a name and not a path, so a corpus can hold "notes" and "notes/one"
// at once and a directory tree cannot. Every document is therefore filed under
// its key plus docSuffix, which is a name no directory will ever take: "notes"
// is the file notes.doc, "notes/one" is one.doc inside the directory notes, and
// both are there at the same time. Without it a corpus on a disk would quietly
// mean less than a corpus in a bucket, and the mount above claims the same
// object-store descriptor over either.
//
// Containment is the operating system's, the way localfs does it: the directory
// is held open as an os.Root and every name is resolved relative to it, so no
// key reaches outside the corpus and neither does a symlink somebody left in
// it.
type LocalDocs struct {
	root *os.Root
}

// docSuffix is what a key is filed under. It keeps a document out of the way of
// the directory a longer key needs, and it is what a listing takes back off to
// read a name as a key again.
const docSuffix = ".doc"

// dirPerm is what a directory a key needed gets when it is made. A document's
// own bits are the mode it was stored with; nothing sets a directory's, because
// no key names one.
const dirPerm = 0o755

// OpenLocal returns the corpus held in an existing host directory, named by an
// absolute path. Opening the directory is what containment rests on, so a
// directory that will not open is an error here rather than at the first write.
func OpenLocal(root string) (*LocalDocs, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open corpus %q: %w", root, err)
	}
	return &LocalDocs{root: opened}, nil
}

// Close gives the directory handle back.
func (d *LocalDocs) Close() error { return d.root.Close() }

// Head describes the document at key from what the disk already knows.
func (d *LocalDocs) Head(_ context.Context, key string) (Entry, error) {
	info, err := d.root.Stat(file(key))
	if err != nil {
		return Entry{}, fmt.Errorf("head %q: %w", key, err)
	}
	return described(key, info), nil
}

// Get returns the document stored at key.
func (d *LocalDocs) Get(_ context.Context, key string) ([]byte, error) {
	body, err := d.root.ReadFile(file(key))
	if err != nil {
		return nil, fmt.Errorf("get %q: %w", key, err)
	}
	return body, nil
}

// Put stores body at key with mode, making whatever directories the key's
// separators ask for. The write is not atomic against a reader: a disk is the
// storage this plugin does the least to, and a corpus is written by one indexer
// rather than raced over.
func (d *LocalDocs) Put(_ context.Context, key string, body []byte, mode fs.FileMode) error {
	name := file(key)
	if dir := path.Dir(name); dir != "." {
		if err := d.root.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("put %q: %w", key, err)
		}
	}
	if err := d.root.WriteFile(name, body, mode.Perm()); err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	// WriteFile leaves an existing file's mode alone, so the bits a caller
	// asked for are set rather than assumed: a document rewritten in a mode it
	// did not have before would otherwise keep the old one.
	if err := d.root.Chmod(name, mode.Perm()); err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	return nil
}

// Move files the document at from under to instead. Rename carries the mode and
// the instant across, which is what the port asks: a document that moved is not
// a document that was written.
//
// The Head is not the rename's own check, which would report the same thing. It
// is what stops a move of nothing from making the directories the landing would
// have needed and leaving them behind empty.
func (d *LocalDocs) Move(ctx context.Context, from, to string) error {
	if _, err := d.Head(ctx, from); err != nil {
		return err
	}
	if dir := path.Dir(file(to)); dir != "." {
		if err := d.root.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("move %q to %q: %w", from, to, err)
		}
	}
	if err := d.root.Rename(file(from), file(to)); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	return nil
}

// Delete removes the document at key. The directories it sat under stay: a
// prefix nothing is under lists nothing either way, and pruning here would race
// a write filing a second document beside it.
func (d *LocalDocs) Delete(_ context.Context, key string) error {
	if err := d.root.Remove(file(key)); err != nil {
		return fmt.Errorf("delete %q: %w", key, err)
	}
	return nil
}

// DeleteAll removes every document whose key starts with prefix, and then the
// directories left holding nothing.
func (d *LocalDocs) DeleteAll(ctx context.Context, prefix string) error {
	found, err := d.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, one := range found {
		if err := d.Delete(ctx, one.Key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete_all %q: %w", prefix, err)
		}
	}
	d.prune(walkFrom(prefix))
	return nil
}

// List describes every document whose key starts with prefix. The walk starts
// at the deepest directory the prefix can be under rather than at the corpus
// root, so listing one part of a corpus does not read all of it.
func (d *LocalDocs) List(_ context.Context, prefix string) ([]Entry, error) {
	var found []Entry
	err := fs.WalkDir(d.root.FS(), walkFrom(prefix), func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(name, docSuffix) {
			return nil
		}
		key := keyOf(name)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		found = append(found, described(key, info))
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("list %q: %w", prefix, err)
	}
	return found, nil
}

// Chmod sets the permission bits the document at key carries.
func (d *LocalDocs) Chmod(_ context.Context, key string, mode fs.FileMode) error {
	if err := d.root.Chmod(file(key), mode.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", key, err)
	}
	return nil
}

// prune removes the directories under from that are left holding nothing,
// deepest first. It is best effort: a directory a write has just put something
// in is one this fails to remove, which is the answer wanted.
func (d *LocalDocs) prune(from string) {
	var dirs []string
	_ = fs.WalkDir(d.root.FS(), from, func(name string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && name != "." {
			dirs = append(dirs, name)
		}
		return nil //nolint:nilerr // a corpus that cannot be walked has nothing to prune
	})
	slices.Reverse(dirs)
	for _, dir := range dirs {
		_ = d.root.Remove(dir)
	}
}

// file is the name on disk a key is filed under.
func file(key string) string { return key + docSuffix }

// keyOf reads a name on disk back as the key it was filed under.
func keyOf(name string) string { return strings.TrimSuffix(name, docSuffix) }

// walkFrom is the directory everything under prefix has to be inside, which is
// where a listing of that prefix can start. The corpus root is what a prefix
// naming nothing deeper answers with.
func walkFrom(prefix string) string {
	dir := path.Dir(path.Clean("/" + prefix))
	if strings.HasSuffix(prefix, separator) {
		dir = path.Clean("/" + prefix)
	}
	if dir == "/" {
		return "."
	}
	return strings.TrimPrefix(dir, "/")
}

// described is what one file on disk is as a listed document.
func described(key string, info fs.FileInfo) Entry {
	return Entry{Key: key, Size: info.Size(), Mode: info.Mode().Perm(), Written: info.ModTime()}
}
