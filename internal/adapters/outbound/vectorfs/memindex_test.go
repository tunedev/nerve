package vectorfs_test

import (
	"context"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// memIndex is a ranking held in a map, computed by cosine similarity in Go. It
// is what makes the plugin's semantics assertable without a container: the case
// that runs against pgvector runs against this one offline, and the two have to
// agree.
//
// The ranking is exact where pgvector's is approximate. That is the point of
// the second implementation: the order two documents come back in is a promise
// of the port, and an index that only returned the right order because it was
// the only index would not be a port at all.
type memIndex struct {
	mu      sync.Mutex
	vectors map[string][]float32
}

func newMemIndex() *memIndex { return &memIndex{vectors: map[string][]float32{}} }

func (i *memIndex) Put(_ context.Context, key string, vector []float32) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if vector == nil {
		delete(i.vectors, key)
		return nil
	}
	i.vectors[key] = slices.Clone(vector)
	return nil
}

func (i *memIndex) Drop(_ context.Context, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	delete(i.vectors, key)
	return nil
}

func (i *memIndex) DropAll(_ context.Context, prefix string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	for key := range i.vectors {
		if strings.HasPrefix(key, prefix) {
			delete(i.vectors, key)
		}
	}
	return nil
}

func (i *memIndex) Move(_ context.Context, from, to string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	vector, filed := i.vectors[from]
	delete(i.vectors, from)
	delete(i.vectors, to)
	if filed {
		i.vectors[to] = vector
	}
	return nil
}

func (i *memIndex) Nearest(_ context.Context, vector []float32, limit int) ([]vectorfs.Hit, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var hits []vectorfs.Hit
	for key, filed := range i.vectors {
		hits = append(hits, vectorfs.Hit{Key: key, Score: cosine(vector, filed)})
	}
	slices.SortFunc(hits, func(a, b vectorfs.Hit) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// cosine is the similarity pgvector's `1 - (a <=> b)` computes, in Go.
func cosine(a, b []float32) float64 {
	var dot, lenA, lenB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		lenA += float64(a[i]) * float64(a[i])
		lenB += float64(b[i]) * float64(b[i])
	}
	if lenA == 0 || lenB == 0 {
		return 0
	}
	return dot / (math.Sqrt(lenA) * math.Sqrt(lenB))
}
