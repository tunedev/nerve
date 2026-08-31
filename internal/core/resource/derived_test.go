package resource_test

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/resource"
)

func mustReadContent(t *testing.T, store *resource.Store, name string) string {
	t.Helper()

	content, err := store.Content(t.Context(), here, name)
	if err != nil {
		t.Fatalf("Content(%q): %v", name, err)
	}
	defer content.Close()

	read, err := io.ReadAll(content)
	if err != nil {
		t.Fatalf("reading %q: %v", name, err)
	}
	return string(read)
}

// A stage owns the resource it was handed and needs no grant to read it. The
// grant is for whoever is outside the process.
func TestContentReadsAFinalisedResourceWithoutAGrant(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")
	mustComplete(t, store, here, "transcript.txt")

	if got := mustReadContent(t, store, "transcript.txt"); got != "a first draft" {
		t.Errorf("Content = %q, want %q", got, "a first draft")
	}
}

// What stands under a resource still arriving is whatever the upload managed so
// far, and a reader would take that for the whole file.
func TestContentRefusesAResourceThatHasNotBeenFinalised(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")

	if _, err := store.Content(t.Context(), here, "transcript.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Content = %v, want fs.ErrNotExist", err)
	}
}

// A sidecar is what something made of the resource, kept beside it, read back
// by whatever comes next.
func TestASidecarIsWrittenBesideTheResourceAndReadBack(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")
	mustComplete(t, store, here, "transcript.txt")

	if err := store.SetDerived(t.Context(), here, "transcript.txt", "text", []byte("a first draft")); err != nil {
		t.Fatalf("SetDerived: %v", err)
	}

	got, err := store.Derived(t.Context(), here, "transcript.txt", "text")
	if err != nil {
		t.Fatalf("Derived: %v", err)
	}
	if string(got) != "a first draft" {
		t.Errorf("Derived = %q, want %q", got, "a first draft")
	}
}

// A run that made it again replaces what the last one left, so re-ingesting a
// resource is the no-op an idempotent pipeline needs it to be.
func TestASidecarWrittenTwiceHoldsTheSecondAnswer(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")
	mustComplete(t, store, here, "transcript.txt")

	for _, text := range []string{"a first draft", "a second draft"} {
		if err := store.SetDerived(t.Context(), here, "transcript.txt", "text", []byte(text)); err != nil {
			t.Fatalf("SetDerived(%q): %v", text, err)
		}
	}

	got, err := store.Derived(t.Context(), here, "transcript.txt", "text")
	if err != nil {
		t.Fatalf("Derived: %v", err)
	}
	if string(got) != "a second draft" {
		t.Errorf("Derived = %q, want the second answer", got)
	}
}

// A sidecar nothing has written yet is not there, which is how a caller tells
// a resource whose ingestion has not run from one whose has.
func TestASidecarNothingWroteIsNotThere(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")
	mustComplete(t, store, here, "transcript.txt")

	if _, err := store.Derived(t.Context(), here, "transcript.txt", "text"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Derived = %v, want fs.ErrNotExist", err)
	}
}

// The two files a resource is made of are not sidecars, and a kind that named
// one of them would write over what the resource says about itself.
func TestASidecarCannotBeNamedAfterTheFilesAResourceIsMadeOf(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")
	mustComplete(t, store, here, "transcript.txt")

	for _, kind := range []string{"meta.json", "content", "", "..", "l0/abstract"} {
		if err := store.SetDerived(t.Context(), here, "transcript.txt", kind, []byte("no")); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("SetDerived(%q) = %v, want fs.ErrInvalid", kind, err)
		}
	}

	if got := mustReadContent(t, store, "transcript.txt"); got != "a first draft" {
		t.Errorf("content after the refusals = %q, want it untouched", got)
	}
	if got, err := store.Stat(t.Context(), here, "transcript.txt"); err != nil || !got.Complete {
		t.Errorf("Stat after the refusals = %+v, %v, want the finalised resource", got, err)
	}
}

// A sidecar belongs to a resource, so one written for a resource nobody
// declared is a stage acting on a name it invented.
func TestASidecarNeedsAResourceToStandBeside(t *testing.T) {
	store := newStore(t, memfs.New())

	err := store.SetDerived(t.Context(), here, "transcript.txt", "text", []byte("no"))

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SetDerived = %v, want fs.ErrNotExist", err)
	}
}

// Deleting a resource takes what was made of it too: a sidecar outliving its
// content is the deletion that did not work.
func TestDeletingAResourceTakesItsSidecars(t *testing.T) {
	store := newStore(t, memfs.New())
	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "a first draft")
	mustComplete(t, store, here, "transcript.txt")
	if err := store.SetDerived(t.Context(), here, "transcript.txt", "text", []byte("a first draft")); err != nil {
		t.Fatalf("SetDerived: %v", err)
	}

	if err := store.Delete(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := store.Derived(t.Context(), here, "transcript.txt", "text"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Derived after Delete = %v, want fs.ErrNotExist", err)
	}
}

// A sidecar is not a resource, so the write that puts one there is not a second
// ingestion of the same file.
func TestASidecarDeclaresNoResource(t *testing.T) {
	store := newStore(t, memfs.New())

	_, _, err := store.Resolve(strings.TrimPrefix(root, "/") + "/ada/hunt/transcript.txt/text")

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Resolve = %v, want fs.ErrNotExist", err)
	}
}
