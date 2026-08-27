package kvfs

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"
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

// Head describes what is stored under key. Over a map it costs what Get costs;
// the port has it for the store that charges a round trip for the content.
func (s *MemStore) Head(_ context.Context, key string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, stored := s.values[key]
	if !stored {
		return Entry{}, fmt.Errorf("head %q: %w", key, fs.ErrNotExist)
	}
	return entry(key, value), nil
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

// Put stores data under key, stamped with the instant it was written. The
// content is copied, so the caller's slice is not the one held.
func (s *MemStore) Put(_ context.Context, key string, data []byte, mode fs.FileMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.values[key] = Value{Data: bytes.Clone(data), Mode: mode, ModTime: time.Now()}
	return nil
}

// Move stores what is under from under to instead. One lock covers both halves,
// so no reader sees the value at neither name or at both.
func (s *MemStore) Move(_ context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	value, stored := s.values[from]
	if !stored {
		return fmt.Errorf("move %q: %w", from, fs.ErrNotExist)
	}
	s.values[to] = value
	delete(s.values, from)
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
