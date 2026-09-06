package streamrotatefs

import (
	"context"
	"fmt"
	"slices"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
)

// Create makes name exist as an empty log. A log already there is left as it is
// rather than truncated: the mount is append-only, and what has been written to
// a log is never shortened.
func (f *FS) Create(_ context.Context, name string) error {
	at, err := keyspace.Key(name)
	if err != nil {
		return fmt.Errorf("create %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.open(at)
	return nil
}

// Read hands back what name holds: the bytes written to it since it last
// rotated, which is less than the whole log once it has. What rotated out is
// read from the segments a listing of the prefix shows.
func (f *FS) Read(_ context.Context, name string) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	held, err := f.find(name)
	if err != nil {
		return nil, fmt.Errorf("read %w", err)
	}
	return slices.Clone(held.content), nil
}

// Write appends data to the end of name, making the log if it is not there and
// sealing a segment if the append filled one.
func (f *FS) Write(_ context.Context, name string, data []byte) error {
	at, err := keyspace.Key(name)
	if err != nil {
		return fmt.Errorf("write %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.append(at, data)
	return nil
}
