package vectorfs_test

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
)

// bagEmbedder is a bag-of-words embedder: every word lands in one bucket and
// counts once there. It is a poor index and an honest one — two texts sharing
// words are close and two sharing none are not — which is all a case asserting
// the order of an answer needs.
//
// It is here rather than in the build for the same reason memIndex is: what the
// mount promises has to be assertable with nothing started.
type bagEmbedder struct{ width int }

func (e bagEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vector := make([]float32, e.width)
		for _, word := range strings.Fields(strings.ToLower(text)) {
			digest := fnv.New32a()
			_, _ = digest.Write([]byte(word))
			vector[digest.Sum32()%uint32(e.width)]++
		}
		vectors[i] = vector
	}
	return vectors, nil
}

// wideEmbedder answers with vectors of a width nobody asked for, which is the
// misconfiguration the mount has to catch: a model behind the gateway that is
// not the model the column was sized for.
type wideEmbedder struct{ width int }

func (e wideEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = make([]float32, e.width)
	}
	return vectors, nil
}

// errEmbedder is an endpoint that will not answer.
type errEmbedder struct{}

var errNoModel = errors.New("no model")

func (errEmbedder) Embed(context.Context, []string) ([][]float32, error) { return nil, errNoModel }
