package graphfs_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"sync"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
)

// memStore is the second side of the Store port: a blob per name in a map. The
// shipped side is the corpus the composition root builds, and holding both to
// the same cases is what makes the semantics above the port assertable with
// nothing started.
type memStore struct {
	mu    sync.Mutex
	blobs map[string][]byte

	// refuse, when set, is what every Save answers with. It is how a case
	// asserts that a change the record did not take is a change the mount did
	// not make.
	refuse error
}

func newMemStore() *memStore { return &memStore{blobs: map[string][]byte{}} }

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

var _ graphfs.Store = newMemStore()
