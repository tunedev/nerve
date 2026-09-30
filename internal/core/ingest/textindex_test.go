package ingest_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/ingest"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// corpusRoot is where the index this file writes into keeps its documents. It
// is a flat key space, as an index's corpus is.
const corpusRoot = "/index/docs"

// indexedInto is the pipeline a build with an index has: extraction, then the
// dispatcher handing the resource to an indexer writing into corpus.
func indexedInto(t *testing.T, b *bus, corpus vfs.FileSystem) {
	t.Helper()

	indexer, err := ingest.NewTextIndexer(b.resources, corpus, corpusRoot)
	if err != nil {
		t.Fatalf("NewTextIndexer: %v", err)
	}
	registry := ingest.NewRegistry()
	if err := registry.Register(upper{}, "text/plain"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b.pipeline = ingest.New(b.resources, b.ledger,
		ingest.Extraction(b.resources, registry),
		ingest.NewDispatch(indexer).Stage())
}

// flatCorpus is a tree with a flat key space mounted where the index keeps its
// documents.
func flatCorpus(t *testing.T) *vfs.MountTable {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(corpusRoot, kvfs.New(kvfs.NewMemStore(), "")); err != nil {
		t.Fatalf("Mount(%q): %v", corpusRoot, err)
	}
	return table
}

// The acceptance: an ingested document's words stand in the index's corpus under
// <user>/<workspace>/<name>, which is the key a reader inside the scope reads it
// back by.
func TestAnIngestedDocumentsWordsAreWrittenIntoTheCorpusUnderItsScope(t *testing.T) {
	b := newBus(t)
	corpus := flatCorpus(t)
	indexedInto(t, b, corpus)

	b.upload(t, "transcript.txt", "a first draft")
	b.work(t)

	got, err := corpus.Read(t.Context(), corpusRoot+"/ada/hunt/transcript.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "A FIRST DRAFT" {
		t.Errorf("the corpus holds %q, want the words extraction left", got)
	}
	if status := b.indexing(t, "transcript.txt").Status; status != ingest.Indexed {
		t.Errorf("the ledger says %q, want %q", status, ingest.Indexed)
	}
}

// A format nothing could read leaves no words, and a document with no words has
// nothing to index. It is left alone rather than failed, for the reason
// extraction left it alone.
func TestADocumentNothingCouldReadIsNotIndexed(t *testing.T) {
	b := newBus(t)
	corpus := flatCorpus(t)
	indexedInto(t, b, corpus)

	b.uploadAs(t, "scan.tiff", "image/tiff", "not words")
	b.work(t)

	if _, err := corpus.Read(t.Context(), corpusRoot+"/ada/hunt/scan.tiff"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read = %v, want nothing indexed", err)
	}
	if status := b.indexing(t, "scan.tiff").Status; status != ingest.Indexed {
		t.Errorf("the ledger says %q, want %q", status, ingest.Indexed)
	}
}

// A corpus that refuses the write is a document that was meant to become
// findable and did not, so the run fails at the index stage and says so.
func TestACorpusThatRefusesTheWriteFailsTheRun(t *testing.T) {
	b := newBus(t)
	tree := vfs.NewMountTable()
	if err := tree.Mount(corpusRoot, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", corpusRoot, err)
	}
	indexedInto(t, b, tree)

	b.upload(t, "transcript.txt", "a first draft")
	err := b.worker(t).Run(t.Context())

	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), corpusRoot) {
		t.Errorf("Run = %v, want the refusal naming the corpus", err)
	}
	got := b.indexing(t, "transcript.txt")
	if got.Status != ingest.Failed || got.Stage != "index" {
		t.Errorf("the ledger says %q at %q, want %q at %q", got.Status, got.Stage, ingest.Failed, "index")
	}
}

// The corpus root is where a mount was put, so it is held to the mount table's
// rule for a prefix.
func TestATextIndexerOverARootThatIsNotAPathIsRefused(t *testing.T) {
	if _, err := ingest.NewTextIndexer(nil, vfs.NewMountTable(), "index/docs"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewTextIndexer(index/docs) = %v, want fs.ErrInvalid", err)
	}
}
