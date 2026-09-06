package graphfs_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
)

// memStore is the second side of both ports: a blob per name in a map, and a
// blob per label beside it. The shipped side is the corpus the composition root
// builds, and holding both to the same cases is what makes the semantics above
// the ports assertable with nothing started.
//
// One type answers both here for the reason one type answers both there: the
// ports are separate because a real graph backend implements one and not the
// other, not because a laptop needs two places.
type memStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
	kept  map[string][]byte

	// refuse, when set, is what every Save answers with. It is how a case
	// asserts that a change the record did not take is a change the mount did
	// not make.
	refuse error
}

func newMemStore() *memStore {
	return &memStore{blobs: map[string][]byte{}, kept: map[string][]byte{}}
}

func (s *memStore) Load(_ context.Context, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, there := s.blobs[name]
	if !there {
		return nil, fmt.Errorf("graph %q: %w", name, fs.ErrNotExist)
	}
	return bytes.Clone(body), nil
}

func (s *memStore) Save(_ context.Context, name string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse != nil {
		return s.refuse
	}
	s.blobs[name] = bytes.Clone(body)
	return nil
}

func (s *memStore) Keep(_ context.Context, graph, label string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse != nil {
		return s.refuse
	}
	s.kept[graph+"/"+label] = bytes.Clone(body)
	return nil
}

func (s *memStore) Fetch(_ context.Context, graph, label string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, there := s.kept[graph+"/"+label]
	if !there {
		return nil, fmt.Errorf("snapshot %q of graph %q: %w", label, graph, fs.ErrNotExist)
	}
	return bytes.Clone(body), nil
}

func (s *memStore) Labels(_ context.Context, graph string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var labels []string
	for key := range s.kept {
		if label, under := strings.CutPrefix(key, graph+"/"); under {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	return labels, nil
}

func (s *memStore) Drop(_ context.Context, graph, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := graph + "/" + label
	if _, there := s.kept[key]; !there {
		return fmt.Errorf("snapshot %q of graph %q: %w", label, graph, fs.ErrNotExist)
	}
	delete(s.kept, key)
	return nil
}

var (
	_ graphfs.Store         = newMemStore()
	_ graphfs.SnapshotStore = newMemStore()
)
