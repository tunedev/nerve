package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// TextIndexer writes the words extraction left into the corpus of an index, where
// writing a document is what makes it findable.
//
// The key is <user>/<workspace>/<name>, derived by the scope's own view of the
// corpus. That is the view a reader inside the scope reads the corpus back
// through, so the key written here and the key read there are one derivation
// rather than two spellings of it.
//
// Nothing here knows what kind of index the corpus belongs to. It is a root on
// the tree, and which mount stands there is config.
type TextIndexer struct {
	resources *resource.Store
	tree      vfs.FileSystem
	corpus    string
}

// NewTextIndexer returns the indexer writing the text of resources in store into
// the corpus under root on tree. root must be a rooted, clean path such as
// "/vectors/docs" — the mount table's rule for a prefix.
func NewTextIndexer(store *resource.Store, tree vfs.FileSystem, root string) (*TextIndexer, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("corpus root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &TextIndexer{resources: store, tree: tree, corpus: root}, nil
}

// Name is the corpus it writes into, which is what a failure needs to say.
func (x *TextIndexer) Name() string { return "text into " + x.corpus }

// Handles claims every resource. Whether one has words is only known by looking,
// and a resource with none is left alone by Index.
func (x *TextIndexer) Handles(resource.Resource) bool { return true }

// Index writes r's text sidecar into the corpus under s.
//
// A resource with no text sidecar is one nothing could read, and is left alone
// rather than failed: no run of the same job would reach a different answer.
func (x *TextIndexer) Index(ctx context.Context, s workspace.Scope, r resource.Resource) error {
	text, err := x.resources.Derived(ctx, s, r.Name, TextSidecar)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	corpus, err := s.View(x.tree, x.corpus)
	if err != nil {
		return err
	}
	return corpus.Write(ctx, path.Join("/", r.Name), text)
}
