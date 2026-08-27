package exportfs_test

import (
	"slices"
	"sync"
	"testing"
)

// Readers arriving together at an export nobody has made yet get one
// materialisation between them rather than one each. That is the whole reason
// the refresh holds the lock across the materialisation, and under the race
// detector this is also the evidence that holding it is safe.
func TestConcurrentReadersMaterialiseAnExportOnce(t *testing.T) {
	const readers = 16

	fsys, made := mount(t, source(t, map[string]string{"/a": "one"}), "/a")

	var running sync.WaitGroup
	running.Add(readers)
	for range readers {
		go func() {
			defer running.Done()
			if content, err := fsys.Read(t.Context(), "/report"); err != nil {
				t.Errorf("Read(/report) = %v", err)
			} else if string(content) != "one" {
				t.Errorf("Read(/report) = %q, want %q", content, "one")
			}
		}()
	}
	running.Wait()

	if runs := made.runs.Load(); runs != 1 {
		t.Errorf("materialisations over %d concurrent first reads = %d, want 1", readers, runs)
	}
}

// A reader never sees an export part way through being made again. Every answer
// is one of the whole values the inputs have held, which is what materialising
// under the lock and handing back a copy is for.
func TestAReaderNeverSeesAHalfRefreshedExport(t *testing.T) {
	const (
		readers = 8
		rounds  = 50
	)
	whole := []string{"one", "a longer one"}

	src := source(t, map[string]string{"/a": whole[0]})
	fsys, _ := mount(t, src, "/a")

	var running sync.WaitGroup
	running.Add(readers + 1)

	go func() {
		defer running.Done()
		for round := range rounds {
			if err := src.Write(t.Context(), "/a", []byte(whole[round%len(whole)])); err != nil {
				t.Errorf("Write(/a) = %v", err)
				return
			}
		}
	}()

	for range readers {
		go func() {
			defer running.Done()
			for range rounds {
				content, err := fsys.Read(t.Context(), "/report")
				if err != nil {
					t.Errorf("Read(/report) = %v", err)
					return
				}
				if !slices.Contains(whole, string(content)) {
					t.Errorf("Read(/report) = %q, want one of %q", content, whole)
					return
				}
			}
		}()
	}
	running.Wait()
}
