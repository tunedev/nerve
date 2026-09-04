package vectorfs_test

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// memIndex is a corpus held in a map, ranked by cosine similarity computed in
// Go. It is what makes the plugin's semantics assertable without a container:
// the suite that runs against pgvector runs against this one offline, and the
// two have to agree.
//
// The ranking is exact where pgvector's is approximate. That is the point of
// the second implementation: the order two documents come back in is a promise
// of the port, and an index that only returned the right order because it was
// the only index would not be a port at all.
type memIndex struct {
	mu   sync.Mutex
	rows map[string]*memRow
}

type memRow struct {
	body    []byte
	vector  []float32
	mode    fs.FileMode
	written time.Time
}

func newMemIndex() *memIndex { return &memIndex{rows: map[string]*memRow{}} }

func (i *memIndex) Put(_ context.Context, key string, body []byte, vector []float32, mode fs.FileMode) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.rows[key] = &memRow{
		body:    slices.Clone(body),
		vector:  slices.Clone(vector),
		mode:    mode,
		written: time.Now(),
	}
	return nil
}

func (i *memIndex) Get(_ context.Context, key string) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	row, stored := i.rows[key]
	if !stored {
		return nil, fmt.Errorf("get %q: %w", key, fs.ErrNotExist)
	}
	return slices.Clone(row.body), nil
}

func (i *memIndex) Peek(_ context.Context, key string) (vectorfs.Entry, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	row, stored := i.rows[key]
	if !stored {
		return vectorfs.Entry{}, fmt.Errorf("peek %q: %w", key, fs.ErrNotExist)
	}
	return row.entry(key), nil
}

func (i *memIndex) List(_ context.Context, prefix string) ([]vectorfs.Entry, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var found []vectorfs.Entry
	for key, row := range i.rows {
		if strings.HasPrefix(key, prefix) {
			found = append(found, row.entry(key))
		}
	}
	slices.SortFunc(found, func(a, b vectorfs.Entry) int { return strings.Compare(a.Key, b.Key) })
	return found, nil
}

func (i *memIndex) Drop(_ context.Context, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if _, stored := i.rows[key]; !stored {
		return fmt.Errorf("drop %q: %w", key, fs.ErrNotExist)
	}
	delete(i.rows, key)
	return nil
}

func (i *memIndex) DropAll(_ context.Context, prefix string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	for key := range i.rows {
		if strings.HasPrefix(key, prefix) {
			delete(i.rows, key)
		}
	}
	return nil
}

func (i *memIndex) Move(_ context.Context, from, to string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	row, stored := i.rows[from]
	if !stored {
		return fmt.Errorf("move %q: %w", from, fs.ErrNotExist)
	}
	delete(i.rows, from)
	i.rows[to] = row
	return nil
}

func (i *memIndex) Chmod(_ context.Context, key string, mode fs.FileMode) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	row, stored := i.rows[key]
	if !stored {
		return fmt.Errorf("chmod %q: %w", key, fs.ErrNotExist)
	}
	row.mode = mode
	return nil
}

func (i *memIndex) Nearest(_ context.Context, vector []float32, limit int) ([]vectorfs.Hit, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var hits []vectorfs.Hit
	for key, row := range i.rows {
		if len(row.vector) == 0 {
			continue
		}
		hits = append(hits, vectorfs.Hit{Key: key, Score: cosine(vector, row.vector)})
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

func (r *memRow) entry(key string) vectorfs.Entry {
	return vectorfs.Entry{
		Key:     key,
		Size:    int64(len(r.body)),
		Mode:    r.mode,
		Written: r.written,
	}
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
