package termvec_test

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termvec"
	"github.com/tunedev/nerve/internal/llmtest"
)

// embedder is one of the given width, or the reason there is none.
func embedder(t *testing.T, width int) termvec.Embedder {
	t.Helper()

	embed, err := termvec.New(width)
	if err != nil {
		t.Fatalf("New(%d) = %v", width, err)
	}
	return embed
}

// vector is what one text embeds to.
func vector(t *testing.T, embed termvec.Embedder, text string) []float32 {
	t.Helper()

	vectors, err := embed.Embed(context.Background(), []string{text})
	if err != nil {
		t.Fatalf("Embed(%q) = %v", text, err)
	}
	if len(vectors) != 1 {
		t.Fatalf("one text embedded to %d vectors", len(vectors))
	}
	return vectors[0]
}

// cosine is how close two vectors are, which is the measure the index this
// feeds ranks by. A vector with no direction has no angle, and this says so
// rather than answering with a number.
func cosine(t *testing.T, a, b []float32) float64 {
	t.Helper()

	var dot, lengthA, lengthB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		lengthA += float64(a[i]) * float64(a[i])
		lengthB += float64(b[i]) * float64(b[i])
	}
	if lengthA == 0 || lengthB == 0 {
		t.Fatal("a vector of nothing but zeros has no angle to measure")
	}
	return dot / (math.Sqrt(lengthA) * math.Sqrt(lengthB))
}

// The port's promises are asserted by the suite both endpoints run, so what is
// left in this file is what this embedder alone promises.
func TestConformance(t *testing.T) {
	llmtest.RunEmbedder(t, embedder(t, 64))
}

// The index this feeds pairs a vector to its text by position and by nothing
// else, so a text embedded in a batch has to be the text embedded alone. The
// width is the one the config asked for, because a mount holding these vectors
// was sized by that number.
func TestABatchEmbedsEachTextAsItself(t *testing.T) {
	embed := embedder(t, 64)

	texts := []string{"the pumps had not run since March", "nobody funded the maintenance"}
	vectors, err := embed.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	for i, text := range texts {
		if len(vectors[i]) != embed.Width() {
			t.Errorf("vector %d is %d wide, want %d", i, len(vectors[i]), embed.Width())
		}
		if cosine(t, vectors[i], vector(t, embed, text)) != 1 {
			t.Errorf("text %d embedded in a batch differs from the same text embedded alone", i)
		}
	}
}

// What this is for: ranking. Two texts sharing vocabulary are closer than two
// sharing none, which is a poor index and an honest one.
func TestSharedVocabularyRanksCloser(t *testing.T) {
	embed := embedder(t, 512)

	question := vector(t, embed, "why did the pumps stop")
	about := vector(t, embed, "the pumps stopped when the maintenance budget went")
	elsewhere := vector(t, embed, "the kitchen roster changed on Tuesday")

	near, far := cosine(t, question, about), cosine(t, question, elsewhere)
	if near <= far {
		t.Errorf("the document about the pumps scored %v and the unrelated one %v, want the pumps closer", near, far)
	}
}

// Words the tokeniser drops are still text, and a document made only of them
// has to land somewhere an index can rank it. A vector of zeros has no angle,
// so cosine distance against it is not a number and the row sorts nowhere.
func TestATextWithNoWordsStillHasADirection(t *testing.T) {
	embed := embedder(t, 64)

	for _, text := range []string{"?!", "a", "   x   "} {
		if _, zero := direction(vector(t, embed, text)); zero {
			t.Errorf("%q embedded to nothing but zeros", text)
		}
	}
}

// Empty text is the one case with nothing to hash. The mount does not embed it
// at all, and what it embeds to here is zeros rather than an invented angle.
func TestEmptyTextEmbedsToZeros(t *testing.T) {
	embed := embedder(t, 64)

	if _, zero := direction(vector(t, embed, "  \n ")); !zero {
		t.Error("whitespace embedded to a direction, want zeros")
	}
}

// direction reports the vector's length and whether it has none.
func direction(v []float32) (float64, bool) {
	var length float64
	for _, at := range v {
		length += float64(at) * float64(at)
	}
	return length, length == 0
}

// A width under one has no bucket to count a word into, so it is refused at
// build time rather than answering every text with a vector of nothing.
func TestAWidthUnderOneIsRefused(t *testing.T) {
	for _, width := range []int{0, -1} {
		if _, err := termvec.New(width); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("New(%d) = %v, want %v", width, err, fs.ErrInvalid)
		}
	}
}
