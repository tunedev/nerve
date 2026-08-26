package memfs_test

import (
	"context"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
)

func TestNewHasAnEmptyRootDirectory(t *testing.T) {
	fsys := memfs.New()

	info, err := fsys.Stat(context.Background(), "/")
	if err != nil {
		t.Fatalf("Stat(/) = %v, want a directory", err)
	}
	if !info.IsDir() {
		t.Errorf("Stat(/).IsDir() = false, want true")
	}

	entries, err := fsys.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/) = %v, want no entries", entries)
	}
}
