package kvfs

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

// MemStore is a keyspace in memory, safe for concurrent use. It is the local
// substitute for a hosted key-value service, behind the same port one would sit
// behind.
type MemStore struct {
	mu     sync.RWMutex
	values map[string]Value
}

// NewMemStore returns a keyspace holding nothing.
func NewMemStore() *MemStore {
	return &MemStore{values: make(map[string]Value)}
}

// Get returns what is stored under key.
func (s *MemStore) Get(_ context.Context, key string) (Value, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, stored := s.values[key]
	if !stored {
		return Value{}, fmt.Errorf("get %q: %w", key, fs.ErrNotExist)
	}
	value.Data = bytes.Clone(value.Data)
	return value, nil
}

// Put stores value under key. The content is copied, so the caller's slice is
// not the one held.
func (s *MemStore) Put(_ context.Context, key string, value Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	value.Data = bytes.Clone(value.Data)
	s.values[key] = value
	return nil
}

// Delete removes key.
func (s *MemStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, stored := s.values[key]; !stored {
		return fmt.Errorf("delete %q: %w", key, fs.ErrNotExist)
	}
	delete(s.values, key)
	return nil
}

// List describes every key starting with prefix. A whole-keyspace scan is what
// a map costs; the port is shaped so a store that can do better does.
func (s *MemStore) List(_ context.Context, prefix string) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var found []Entry
	for key, value := range s.values {
		if strings.HasPrefix(key, prefix) {
			found = append(found, entry(key, value))
		}
	}
	return found, nil
}
