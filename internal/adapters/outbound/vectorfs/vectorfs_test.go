package vectorfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// width is how wide the offline cases embed to, and neighbours how many
// documents a question is answered with. Both are small on purpose: a case
// asserting an order wants an answer it can read.
const (
	width      = 64
	neighbours = 3
)

// newIndex returns an index mount over a corpus on the disk and a ranking in a
// map, embedded by the bag of words.
func newIndex(t *testing.T) *vectorfs.FS {
	t.Helper()
	return newIndexOver(t, newLocalDocs(t), newMemIndex(), bagEmbedder{width: width})
}

// newIndexOver returns an index mount over the corpus, ranking and model given,
// which is what a case naming one of the three builds through.
func newIndexOver(t *testing.T, docs vectorfs.DocStore, index vectorfs.Index, embed llm.Embedder) *vectorfs.FS {
	t.Helper()
	return vectorfs.New("test", width, neighbours, docs, index, embed)
}

// The acceptance: a question read through the tree comes back as the corpus
// ranked against it, closest first. Nothing here holds an index, a model or a
// query language — a path was written and a path was read.
func TestAQuestionAnswersWithTheCorpusRanked(t *testing.T) {
	fsys := newIndex(t)
	ctx := t.Context()

	corpus := map[string]string{
		"retry":   "a retry repeats a call that failed for a reason that may pass",
		"breaker": "a circuit breaker stops calling a dependency that keeps failing",
		"pasta":   "boil the water before the pasta goes in",
	}
	for key, text := range corpus {
		if err := fsys.Write(ctx, "/docs/"+key, []byte(text)); err != nil {
			t.Fatalf("Write(/docs/%s) = %v", key, err)
		}
	}

	ranked := answer(t, fsys, "a retry repeats a call that failed")
	if len(ranked) == 0 {
		t.Fatal("a question of a corpus with three documents answered with none")
	}
	if ranked[0] != "retry" {
		t.Errorf("the closest document was %q, want %q", ranked[0], "retry")
	}
	if last := ranked[len(ranked)-1]; last != "pasta" {
		t.Errorf("the furthest document was %q, want %q", last, "pasta")
	}
}

// A question is answered with at most as many documents as the mount was
// configured for, however large the corpus is. The ceiling is config because
// how much of an answer a caller can hold is operational.
func TestAQuestionAnswersWithAtMostTheNeighboursConfigured(t *testing.T) {
	fsys := newIndex(t)
	ctx := t.Context()

	for _, key := range []string{"a", "b", "c", "d", "e"} {
		if err := fsys.Write(ctx, "/docs/"+key, []byte("a call that failed "+key)); err != nil {
			t.Fatalf("Write(/docs/%s) = %v", key, err)
		}
	}

	if ranked := answer(t, fsys, "a call that failed"); len(ranked) != neighbours {
		t.Errorf("a question of five documents answered with %d, want %d", len(ranked), neighbours)
	}
}

// An index with nothing in it answers rather than failing. "Nothing matched"
// and "there is no such path" are different answers, and only the first is true
// of a question asked of an empty corpus.
func TestAQuestionOfAnEmptyIndexAnswersWithNoDocuments(t *testing.T) {
	fsys := newIndex(t)

	if ranked := answer(t, fsys, "anything at all"); len(ranked) != 0 {
		t.Errorf("an empty corpus answered with %v, want nothing", ranked)
	}
}

// A document with nothing in it has nothing to be similar to, so it is stored
// and never returned. The alternative is a zero vector that is equally close to
// every question.
func TestAnEmptyDocumentIsNeverAnAnswer(t *testing.T) {
	fsys := newIndex(t)
	ctx := t.Context()

	if err := fsys.Create(ctx, "/docs/blank"); err != nil {
		t.Fatalf("Create(/docs/blank) = %v", err)
	}
	if err := fsys.Write(ctx, "/docs/said", []byte("something worth finding")); err != nil {
		t.Fatalf("Write(/docs/said) = %v", err)
	}

	if _, err := fsys.Stat(ctx, "/docs/blank"); err != nil {
		t.Fatalf("Stat(/docs/blank) = %v, want the empty document to be there", err)
	}
	for _, key := range answer(t, fsys, "something worth finding") {
		if key == "blank" {
			t.Error("the empty document was ranked against a question")
		}
	}
}

// The two places that know how wide a vector is cannot check each other: the
// column was sized from mount config and the model reports nothing about
// itself. So the mount checks, at its own edge, and says both numbers.
func TestAVectorOfTheWrongWidthIsRefused(t *testing.T) {
	fsys := newIndexOver(t, newLocalDocs(t), newMemIndex(), wideEmbedder{width: width + 1})

	err := fsys.Write(t.Context(), "/docs/one", []byte("text"))
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Write with a mismatched model = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "65") || !strings.Contains(err.Error(), "64") {
		t.Errorf("the refusal was %q, want it to name both widths", err)
	}
}

// A model that will not answer is a write that did not happen. The alternative
// is a document in the corpus that no question can reach.
func TestADocumentIsNotStoredWhenItCannotBeEmbedded(t *testing.T) {
	fsys := newIndexOver(t, newLocalDocs(t), newMemIndex(), errEmbedder{})
	ctx := t.Context()

	if err := fsys.Write(ctx, "/docs/one", []byte("text")); !errors.Is(err, errNoModel) {
		t.Fatalf("Write with a failing model = %v, want the model's failure", err)
	}
	if _, err := fsys.Stat(ctx, "/docs/one"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat after a failed embedding = %v, want fs.ErrNotExist", err)
	}
}

// A rename changes where a document is filed and not what it says, so the
// vector travels with it rather than being made again.
func TestARenameKeepsTheDocumentFindable(t *testing.T) {
	docs, index := newLocalDocs(t), newMemIndex()
	fsys := newIndexOver(t, docs, index, bagEmbedder{width: width})
	ctx := t.Context()

	if err := fsys.Write(ctx, "/docs/here", []byte("a circuit breaker stops calling")); err != nil {
		t.Fatalf("Write(/docs/here) = %v", err)
	}

	// The model is taken away, so a rename that embedded again would fail.
	moved := newIndexOver(t, docs, index, errEmbedder{})
	if err := moved.Rename(ctx, "/docs/here", "/docs/there"); err != nil {
		t.Fatalf("Rename = %v, want a rename that asks no model", err)
	}

	ranked := answer(t, fsys, "a circuit breaker stops calling")
	if len(ranked) != 1 || ranked[0] != "there" {
		t.Errorf("after the rename the corpus ranked %v, want the document under its new name", ranked)
	}
}

// The descriptor is the path and not the kind. One mount takes documents under
// one path, answers questions under another and refuses to be written at
// either of the other two, and no type could have said all three.
func TestCapabilitiesVaryWithinTheMount(t *testing.T) {
	fsys := newIndex(t)

	corpus := vfs.Caps{ObjectStore: true}
	for _, name := range []string{"/docs", "/docs/one", "/docs/deep/absent"} {
		if got := fsys.Capabilities(name); got != corpus {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, got, corpus)
		}
	}

	fixed := vfs.Caps{ReadOnly: true}
	for _, name := range []string{"/", "/config", "/query", "/query/a question", "/elsewhere"} {
		if got := fsys.Capabilities(name); got != fixed {
			t.Errorf("Capabilities(%q) = %+v, want %+v", name, got, fixed)
		}
	}
}

// Nothing is stored under the questions, so there is nothing to enumerate. A
// listing of what somebody happened to ask would be a second thing the mount
// held, and it holds one.
func TestTheQuestionsListNothingAndAnswerAnything(t *testing.T) {
	fsys := newIndex(t)
	ctx := t.Context()

	if err := fsys.Write(ctx, "/docs/one", []byte("a call that failed")); err != nil {
		t.Fatalf("Write(/docs/one) = %v", err)
	}

	entries, err := fsys.ReadDir(ctx, "/query")
	if err != nil {
		t.Fatalf("ReadDir(/query) = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/query) = %v, want nothing to enumerate", entries)
	}
	for _, question := range []string{"a call that failed", "anything else", "a/question/with/separators"} {
		if _, err := fsys.Read(ctx, "/query/"+question); err != nil {
			t.Errorf("Read(/query/%s) = %v, want an answer", question, err)
		}
	}
}

// The mount's own shape is not a caller's to rearrange. Writing beside or over
// the three paths it holds is refused rather than quietly creating a fourth.
func TestTheMountsShapeIsRefused(t *testing.T) {
	fsys := newIndex(t)
	ctx := t.Context()

	refusals := map[string]error{
		"write outside":   fsys.Write(ctx, "/elsewhere", []byte("x")),
		"write a config":  fsys.Write(ctx, "/config", []byte("x")),
		"write a query":   fsys.Write(ctx, "/query/a question", []byte("x")),
		"remove a query":  fsys.Remove(ctx, "/query/a question"),
		"rename a query":  fsys.Rename(ctx, "/query/a question", "/docs/one"),
		"chmod a query":   fsys.Chmod(ctx, "/query/a question", 0o600),
		"mkdir a query":   fsys.Mkdir(ctx, "/query/a question"),
		"remove the docs": fsys.Remove(ctx, "/docs"),
	}
	for what, err := range refusals {
		if err == nil {
			t.Errorf("%s was allowed, want a refusal", what)
		}
	}
}

// The config path describes the index and is read rather than computed, which
// is what makes it worth having beside the questions.
func TestTheConfigPathDescribesTheIndex(t *testing.T) {
	fsys := newIndex(t)

	described, err := fsys.Read(t.Context(), "/config")
	if err != nil {
		t.Fatalf("Read(/config) = %v", err)
	}
	for _, line := range []string{"index test", "dimension 64", "neighbours 3"} {
		if !strings.Contains(string(described), line) {
			t.Errorf("Read(/config) = %q, want it to carry %q", described, line)
		}
	}
}

// answer reads a question through the tree and returns the keys it ranked, best
// first.
func answer(t *testing.T, fsys *vectorfs.FS, question string) []string {
	t.Helper()

	answered, err := fsys.Read(t.Context(), "/query/"+question)
	if err != nil {
		t.Fatalf("Read(/query/%s) = %v", question, err)
	}
	return rankedKeys(t, answered)
}

// rankedKeys reads the keys out of an answer, best first. It is what every case
// asserting an order goes through, offline and against pgvector, so the format
// an answer arrives in is asserted once, here.
func rankedKeys(t *testing.T, answered []byte) []string {
	t.Helper()

	lines := strings.Split(strings.TrimSuffix(string(answered), "\n"), "\n")
	if lines[0] != "key\tscore" {
		t.Fatalf("an answer began %q, want a heading naming its columns", lines[0])
	}

	keys := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		key, score, split := strings.Cut(line, "\t")
		if !split || score == "" {
			t.Fatalf("an answer carried %q, want a key and a score", line)
		}
		keys = append(keys, key)
	}
	return keys
}
