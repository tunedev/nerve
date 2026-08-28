package library_test

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/library"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The verbs are exercised on one kind here and on every kind in kind_test.go.
// Skills are the kind used, because the questions these tests ask are about the
// shape rather than about what a skill is.
const root = "/skills"

var (
	skills   = library.Skills()
	document = skills.Filename

	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
)

func newRegistry(t *testing.T) (*library.Registry, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	registry, err := library.NewRegistry(table, skills, root, termmatch.New())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry, table
}

func mustCreate(t *testing.T, r *library.Registry, s workspace.Scope, name, description, body string) {
	t.Helper()
	doc := library.Document{Card: library.Card{Name: name, Description: description}, Body: body}
	if err := r.Create(t.Context(), s, doc); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
}

// mustResource puts a file beside a document, which is what makes a document a
// directory rather than a file.
func mustResource(t *testing.T, fsys vfs.FileSystem, root string, s workspace.Scope, name, rel, content string) {
	t.Helper()

	full, err := s.Path(root, path.Join(name, rel))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := fsys.Mkdir(t.Context(), path.Dir(full)); err != nil {
		t.Fatalf("Mkdir(%q): %v", path.Dir(full), err)
	}
	if err := fsys.Write(t.Context(), full, []byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", full, err)
	}
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	registry, _ := newRegistry(t)
	mustCreate(t, registry, here, "rewrite-cv", "Rewrite a CV for one job advert.", "Read the advert.\nThen the CV.\n")

	got, err := registry.Get(t.Context(), here, "rewrite-cv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := library.Document{
		Card: library.Card{Name: "rewrite-cv", Description: "Rewrite a CV for one job advert."},
		Body: "Read the advert.\nThen the CV.\n",
	}
	if got != want {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
}

// A second Create is how a skill is edited: the verb set has no update, and the
// files beside the document are not part of what is being said again.
func TestCreateReplacesTheDocumentAndKeepsTheFiles(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "rewrite-cv", "First.", "old")
	mustResource(t, fsys, root, here, "rewrite-cv", "refs/tone.md", "plain")
	mustCreate(t, registry, here, "rewrite-cv", "Second.", "new")

	got, err := registry.Get(t.Context(), here, "rewrite-cv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "Second." || got.Body != "new" {
		t.Fatalf("Get = %+v, want the second document", got)
	}

	names, err := registry.Tree(t.Context(), here, "rewrite-cv")
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if !slices.Contains(names, "refs/tone.md") {
		t.Fatalf("Tree = %v, want the file beside the document to survive", names)
	}
}

func TestGetMissingSkillDoesNotExist(t *testing.T) {
	registry, _ := newRegistry(t)
	if _, err := registry.Get(t.Context(), here, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get = %v, want fs.ErrNotExist", err)
	}
}

func TestListSortsCardsAndReadsNoBodies(t *testing.T) {
	registry, _ := newRegistry(t)
	mustCreate(t, registry, here, "tailor", "Tailor a CV.", "body")
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")

	cards, err := registry.List(t.Context(), here)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []library.Card{
		{Name: "apply", Description: "Apply to an advert."},
		{Name: "tailor", Description: "Tailor a CV."},
	}
	if !slices.Equal(cards, want) {
		t.Fatalf("List = %+v, want %+v", cards, want)
	}
}

func TestListIsEmptyBeforeAnySkillExists(t *testing.T) {
	registry, _ := newRegistry(t)
	cards, err := registry.List(t.Context(), here)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("List = %+v, want none", cards)
	}
}

// A directory in the library that holds no document is not a skill, and a
// document that will not decode is a skill someone has to fix.
func TestListPassesOverANonSkillAndFailsOnABrokenOne(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")
	mustResource(t, fsys, root, here, "scratch", "note.txt", "not a skill")

	cards, err := registry.List(t.Context(), here)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cards) != 1 || cards[0].Name != "apply" {
		t.Fatalf("List = %+v, want only the skill", cards)
	}

	mustResource(t, fsys, root, here, "broken", document, "no frontmatter here")
	if _, err := registry.List(t.Context(), here); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("List = %v, want fs.ErrInvalid", err)
	}
}

func TestDeleteRemovesTheWholeSkillAndIsIdempotent(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")
	mustResource(t, fsys, root, here, "apply", "refs/tone.md", "plain")

	for range 2 {
		if err := registry.Delete(t.Context(), here, "apply"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	if _, err := registry.Get(t.Context(), here, "apply"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get after Delete = %v, want fs.ErrNotExist", err)
	}

	gone, err := here.Path(root, "apply/refs/tone.md")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := fsys.Stat(t.Context(), gone); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(%q) = %v, want the files beside the document to be gone", gone, err)
	}
}

func TestTreeIsEveryFileTheSkillIsMadeOf(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")
	mustResource(t, fsys, root, here, "apply", "refs/tone.md", "plain")
	mustResource(t, fsys, root, here, "apply", "refs/deep/example.txt", "example")

	names, err := registry.Tree(t.Context(), here, "apply")
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	want := []string{document, "refs/deep/example.txt", "refs/tone.md"}
	if !slices.Equal(names, want) {
		t.Fatalf("Tree = %v, want %v", names, want)
	}
}

func TestTreeOfAMissingSkillDoesNotExist(t *testing.T) {
	registry, _ := newRegistry(t)
	if _, err := registry.Tree(t.Context(), here, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Tree = %v, want fs.ErrNotExist", err)
	}
}

func TestBundleCarriesTheSkillAndTheFilesBesideIt(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")
	mustResource(t, fsys, root, here, "apply", "refs/tone.md", "plain")

	bundle, err := registry.Bundle(t.Context(), here, "apply")
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if bundle.Document.Body != "body" || bundle.Document.Name != "apply" {
		t.Fatalf("Bundle skill = %+v, want the whole skill", bundle.Document)
	}
	if len(bundle.Files) != 1 || bundle.Files[0].Path != "refs/tone.md" || string(bundle.Files[0].Data) != "plain" {
		t.Fatalf("Bundle files = %+v, want the one file beside the document", bundle.Files)
	}
}

func TestCopyTakesTheWholeSkillToAnotherWorkspace(t *testing.T) {
	registry, fsys := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")
	mustResource(t, fsys, root, here, "apply", "refs/tone.md", "plain")

	if err := registry.Copy(t.Context(), here, "apply", there, "applying"); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	got, err := registry.Get(t.Context(), there, "applying")
	if err != nil {
		t.Fatalf("Get after Copy: %v", err)
	}
	if got.Name != "applying" || got.Description != "Apply to an advert." || got.Body != "body" {
		t.Fatalf("Get after Copy = %+v, want the copy under its new name", got)
	}

	names, err := registry.Tree(t.Context(), there, "applying")
	if err != nil {
		t.Fatalf("Tree after Copy: %v", err)
	}
	if !slices.Equal(names, []string{document, "refs/tone.md"}) {
		t.Fatalf("Tree after Copy = %v, want the files too", names)
	}

	// The source is a copy, not a move.
	if _, err := registry.Get(t.Context(), here, "apply"); err != nil {
		t.Fatalf("Get source after Copy: %v", err)
	}
}

func TestCopyOntoAnExistingSkillIsRefused(t *testing.T) {
	registry, _ := newRegistry(t)
	mustCreate(t, registry, here, "apply", "Apply to an advert.", "body")
	mustCreate(t, registry, there, "apply", "Something else.", "other")

	if err := registry.Copy(t.Context(), here, "apply", there, "apply"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Copy = %v, want fs.ErrExist", err)
	}
	got, err := registry.Get(t.Context(), there, "apply")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != "other" {
		t.Fatalf("Get = %+v, want the refused copy to have changed nothing", got)
	}
}

func TestCopyOfAMissingSkillDoesNotExist(t *testing.T) {
	registry, _ := newRegistry(t)
	if err := registry.Copy(t.Context(), here, "absent", there, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Copy = %v, want fs.ErrNotExist", err)
	}
}

// A name that is not one flat segment would put a skill somewhere List cannot
// see it, or somewhere another scope can.
func TestNamesThatAreNotOneSegmentAreRefused(t *testing.T) {
	registry, _ := newRegistry(t)
	for _, name := range []string{"", ".", "..", "refs/tone", "../escape"} {
		doc := library.Document{Card: library.Card{Name: name, Description: "d"}, Body: "b"}
		if err := registry.Create(t.Context(), here, doc); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Create(%q) = %v, want fs.ErrInvalid", name, err)
		}
	}
}

func TestASkillWithoutADescriptionIsRefused(t *testing.T) {
	registry, _ := newRegistry(t)
	for _, description := range []string{"", "two\nlines"} {
		doc := library.Document{Card: library.Card{Name: "apply", Description: description}, Body: "b"}
		if err := registry.Create(t.Context(), here, doc); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Create(%q) = %v, want fs.ErrInvalid", description, err)
		}
	}
}

// User scope holds workspaces, so a library there would report workspaces as
// skills and put skills where a workspace belongs.
func TestUserScopeIsNotASkillLibrary(t *testing.T) {
	registry, _ := newRegistry(t)
	user := workspace.Scope{User: "ada"}

	if _, err := registry.List(t.Context(), user); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("List in user scope = %v, want fs.ErrInvalid", err)
	}
	doc := library.Document{Card: library.Card{Name: "apply", Description: "d"}, Body: "b"}
	if err := registry.Create(t.Context(), user, doc); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Create in user scope = %v, want fs.ErrInvalid", err)
	}
}

func TestARegistryWithoutAMatcherOrARootIsRefused(t *testing.T) {
	table := vfs.NewMountTable()
	if _, err := library.NewRegistry(table, skills, root, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("NewRegistry with no matcher = %v, want fs.ErrInvalid", err)
	}
	if _, err := library.NewRegistry(table, skills, "skills/", termmatch.New()); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("NewRegistry with a relative root = %v, want fs.ErrInvalid", err)
	}
}

// The document fences its frontmatter, and a body is free to contain the fence.
// Only the first one closes the block.
func TestABodyMayHoldAnythingIncludingTheFence(t *testing.T) {
	registry, _ := newRegistry(t)
	for _, body := range []string{"", "---\n", "one\n---\ntwo\n", "---\ndescription: not this one\n---\n"} {
		mustCreate(t, registry, here, "apply", "Apply to an advert.", body)

		got, err := registry.Get(t.Context(), here, "apply")
		if err != nil {
			t.Fatalf("Get(%q): %v", body, err)
		}
		if got.Body != body || got.Description != "Apply to an advert." {
			t.Fatalf("Get = %+v, want the body back unchanged", got)
		}
	}
}
