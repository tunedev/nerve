package vectorfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Create makes an empty document at name, replacing what was there.
//
// An empty document has nothing to embed, so it gets no vector and no question
// ever answers with it. That is not a special case bolted on: a similarity to a
// document that says nothing is not a number anyone can act on.
func (f *FS) Create(ctx context.Context, name string) error {
	return f.Write(ctx, name, nil)
}

// Mkdir has nothing to make under the corpus: no directory exists apart from
// the documents under it, and saying so is more useful than refusing a verb a
// caller cannot know to skip. The mount's own three paths are not a caller's to
// make, so anywhere else this is a refusal.
func (f *FS) Mkdir(_ context.Context, name string) error {
	if spot, _ := locate(name); spot != atDoc {
		return fmt.Errorf("mkdir %q: an index holds documents, not directories: %w", name, fs.ErrPermission)
	}
	return nil
}

// Write puts a document at name and files the vector made from it beside it.
//
// The embedding happens before either store is touched, so a model that will
// not answer is a write that did not happen. The alternative is a corpus that
// quietly stops being findable while every write reports success.
//
// The document is stored and then indexed, which is the order the two ports'
// standing decides: the corpus is the record, so a failure between them leaves
// a document that is there and not yet findable, and writing it again is what
// repairs that. The other order would leave the ranking naming a document
// nobody can read.
//
// The mode survives the write, so a mode Chmod set is a mode a later write does
// not silently reset.
func (f *FS) Write(ctx context.Context, name string, data []byte) error {
	spot, key := locate(name)
	if spot != atDoc {
		return f.refuse("write", name, spot)
	}

	mode, err := f.modeOf(ctx, key)
	if err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	vector, err := f.vectorOf(ctx, data)
	if err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	if err := f.docs.Put(ctx, f.doc(key), data, mode); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	if err := f.index.Put(ctx, key, vector); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	return nil
}

// Read hands back what is at name.
//
// Under the corpus it is the document, unchanged and as often as it is asked
// for. Under the questions it is the corpus ranked against the question, which
// is the whole point of the mount: an approximate-nearest-neighbour query
// reached through the same verb that reads a file.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	spot, rest := locate(name)
	switch spot {
	case atConfig:
		return f.description(), nil
	case atDoc:
		body, err := f.docs.Get(ctx, f.doc(rest))
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", name, err)
		}
		return body, nil
	case atQuestion:
		answer, err := f.answer(ctx, rest)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", name, err)
		}
		return answer, nil
	case atRoot, atCorpus, atQuery:
		return nil, fmt.Errorf("read %q: a directory: %w", name, fs.ErrInvalid)
	default:
		return nil, fmt.Errorf("read %q: %w", name, fs.ErrNotExist)
	}
}

// modeOf is the mode a write to key stores under: the one the document already
// carries, or the default when nothing is stored there yet.
func (f *FS) modeOf(ctx context.Context, key string) (fs.FileMode, error) {
	entry, err := f.docs.Head(ctx, f.doc(key))
	switch {
	case err == nil:
		return entry.Mode.Perm(), nil
	case errors.Is(err, fs.ErrNotExist):
		return fileMode, nil
	default:
		return 0, err
	}
}

// refuse is what every mutating verb says about a path that is not under the
// corpus. The mount's shape is fixed, so the refusal is a permission and not a
// missing entry: there is nothing a caller could create first to make it work.
func (f *FS) refuse(verb, name string, where spot) error {
	if where == outside {
		return fmt.Errorf("%s %q: an index holds %q, %q and %q: %w",
			verb, name, Docs, Query, Config, fs.ErrPermission)
	}
	return fmt.Errorf("%s %q: the shape of the mount: %w", verb, name, fs.ErrPermission)
}
