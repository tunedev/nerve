// Package llmtest is the conformance suite a model endpoint runs. It is one
// test set rather than one per adapter: what a capability promises is asserted
// here, so a second endpoint serving it means running the suite against it
// instead of writing a second set of cases that can disagree with the first.
//
// An adapter hands its endpoint over and the suite drives it:
//
//	func TestConformance(t *testing.T) {
//		llmtest.RunEmbedder(t, embedder)
//	}
//
// Only what the port promises is asserted, which is less than what an endpoint
// does. Nothing here says a vector is any good — that is the model's business
// and there is no assertion to make about a number — and nothing says two
// endpoints agree, because they do not: vectors are comparable only with
// vectors from the endpoint that made them.
package llmtest

import (
	"context"
	"testing"

	"github.com/tunedev/nerve/internal/core/llm"
)

// RunEmbedder holds an embedding endpoint to what llm.Embedder promises: one
// vector per text, in the order the texts were given, every vector one width,
// and the same text embedded the same way twice.
//
// The endpoint is called, so a suite over a remote one runs only where that one
// is reachable. Every case is a subtest, so a failure names the promise that
// was broken rather than the line that noticed.
func RunEmbedder(t *testing.T, embed llm.Embedder) {
	t.Helper()

	t.Run("one vector per text", func(t *testing.T) {
		texts := []string{"the pumps had not run since March", "nobody funded the maintenance"}
		vectors := embedded(t, embed, texts)

		if len(vectors) != len(texts) {
			t.Fatalf("Embed returned %d vectors for %d texts", len(vectors), len(texts))
		}
	})

	t.Run("every vector is one width", func(t *testing.T) {
		vectors := embedded(t, embed, []string{"the pumps had not run since March", "boil the water before the pasta goes in"})

		if len(vectors[0]) == 0 {
			t.Fatal("Embed returned a vector of nothing, which no index can hold")
		}
		if len(vectors[0]) != len(vectors[1]) {
			t.Errorf("Embed returned vectors of %d and %d, want one width", len(vectors[0]), len(vectors[1]))
		}
	})

	t.Run("the same text embeds the same way twice", func(t *testing.T) {
		text := []string{"the pumps had not run since March"}
		first, again := embedded(t, embed, text), embedded(t, embed, text)

		if len(first[0]) != len(again[0]) {
			t.Fatalf("one text embedded to %d dimensions and then %d", len(first[0]), len(again[0]))
		}
		for i := range first[0] {
			if first[0][i] != again[0][i] {
				t.Fatalf("dimension %d embedded to %v and then %v; a vector written today is compared against one read next week", i, first[0][i], again[0][i])
			}
		}
	})

	t.Run("no texts is no vectors", func(t *testing.T) {
		if vectors := embedded(t, embed, nil); len(vectors) != 0 {
			t.Errorf("Embed(nothing) = %v, want no vectors", vectors)
		}
	})
}

// embedded is what the endpoint answers, or the reason the case stops here.
func embedded(t *testing.T, embed llm.Embedder, texts []string) [][]float32 {
	t.Helper()

	vectors, err := embed.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed(%q) = %v", texts, err)
	}
	return vectors
}
