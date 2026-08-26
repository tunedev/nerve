package memfs_test

import (
	"context"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

// The port contract is asserted once, in the conformance suite. What is left in
// this package is what only memfs promises, and these two helpers are what
// those tests build a tree with.
func mustWrite(t *testing.T, fsys *memfs.FS, name, content string) {
	t.Helper()
	if err := fsys.Write(context.Background(), name, []byte(content)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func mustMkdir(t *testing.T, fsys *memfs.FS, name string) {
	t.Helper()
	if err := fsys.Mkdir(context.Background(), name); err != nil {
		t.Fatalf("Mkdir(%q) = %v", name, err)
	}
}
