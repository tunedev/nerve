package library_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/core/library"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The kinds every verb is asked about. Two of them are the ones the package
// names; the third is invented here and the package has never heard of it,
// which is what makes this a test of the shape rather than of the two.
var kinds = []library.Kind{
	library.Skills(),
	library.Blueprints(),
	{Name: "checklist", Filename: "CHECKLIST.md"},
}

// newKind returns a registry of kind over a mount of its own at root.
func newKind(t *testing.T, kind library.Kind, root string) (*library.Registry, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	registry, err := library.NewRegistry(table, kind, root, termmatch.New())
	if err != nil {
		t.Fatalf("NewRegistry(%q): %v", kind.Name, err)
	}
	return registry, table
}

// The acceptance: a blueprint is a skill's shape with a different file on it,
// so every verb answers for it, and for a kind nobody has written code for.
func TestEveryVerbAnswersForEveryKind(t *testing.T) {
	for _, kind := range kinds {
		t.Run(kind.Name, func(t *testing.T) {
			area := "/" + kind.Name
			registry, fsys := newKind(t, kind, area)

			mustCreate(t, registry, here, "cover-letter", "Write a cover letter for one advert.", "State the role.\n")
			mustResource(t, fsys, area, here, "cover-letter", "examples/short.md", "an example")

			got, err := registry.Get(t.Context(), here, "cover-letter")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Description != "Write a cover letter for one advert." || got.Body != "State the role.\n" {
				t.Fatalf("Get = %+v, want what was created", got)
			}

			cards, err := registry.List(t.Context(), here)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(cards) != 1 || cards[0].Name != "cover-letter" {
				t.Fatalf("List = %+v, want the one document", cards)
			}

			matches, err := registry.Search(t.Context(), here, "write a cover letter", 0)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(matches) != 1 || matches[0].Card.Name != "cover-letter" {
				t.Fatalf("Search = %+v, want the document for the task", matches)
			}

			names, err := registry.Tree(t.Context(), here, "cover-letter")
			if err != nil {
				t.Fatalf("Tree: %v", err)
			}
			if !slices.Equal(names, []string{kind.Filename, "examples/short.md"}) {
				t.Fatalf("Tree = %v, want the kind's own file and the one beside it", names)
			}

			bundle, err := registry.Bundle(t.Context(), here, "cover-letter")
			if err != nil {
				t.Fatalf("Bundle: %v", err)
			}
			if bundle.Document.Name != "cover-letter" || len(bundle.Files) != 1 {
				t.Fatalf("Bundle = %+v, want the document and the file beside it", bundle)
			}

			if err := registry.Copy(t.Context(), here, "cover-letter", there, "outreach"); err != nil {
				t.Fatalf("Copy: %v", err)
			}
			if _, err := registry.Get(t.Context(), there, "outreach"); err != nil {
				t.Fatalf("Get after Copy: %v", err)
			}

			if err := registry.Delete(t.Context(), here, "cover-letter"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := registry.Get(t.Context(), here, "cover-letter"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Get after Delete = %v, want fs.ErrNotExist", err)
			}
		})
	}
}

// A kind is not a root. Two libraries sharing one mount hold their own
// documents and nothing of each other's: the file in the directory says which
// library the directory belongs to, and a directory belonging to the other one
// is passed over the way anything else standing in the tree is.
func TestALibrarySeesOnlyItsOwnKind(t *testing.T) {
	area := "/library"
	table := vfs.NewMountTable()
	if err := table.Mount(area, memfs.New()); err != nil {
		t.Fatalf("Mount(%q): %v", area, err)
	}

	skillsOf, err := library.NewRegistry(table, library.Skills(), area, termmatch.New())
	if err != nil {
		t.Fatalf("NewRegistry(skills): %v", err)
	}
	blueprintsOf, err := library.NewRegistry(table, library.Blueprints(), area, termmatch.New())
	if err != nil {
		t.Fatalf("NewRegistry(blueprints): %v", err)
	}

	mustCreate(t, skillsOf, here, "tailor-cv", "Rewrite a CV to answer one advert.", "body")
	mustCreate(t, blueprintsOf, here, "cover-letter", "What a cover letter is made of.", "body")

	for _, tc := range []struct {
		registry *library.Registry
		want     string
	}{
		{skillsOf, "tailor-cv"},
		{blueprintsOf, "cover-letter"},
	} {
		cards, err := tc.registry.List(t.Context(), here)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(cards) != 1 || cards[0].Name != tc.want {
			t.Fatalf("List = %+v, want only %q", cards, tc.want)
		}
	}

	if _, err := blueprintsOf.Get(t.Context(), here, "tailor-cv"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get of a skill through the blueprints = %v, want fs.ErrNotExist", err)
	}
}

// A kind claims one file in a directory and everything else is beside it, so a
// document of another kind standing in the same directory travels as one of its
// files rather than as a second document.
func TestAnotherKindsFileIsAFileBesideThisOne(t *testing.T) {
	area := "/library"
	registry, fsys := newKind(t, library.Blueprints(), area)

	mustCreate(t, registry, here, "cover-letter", "What a cover letter is made of.", "body")
	mustResource(t, fsys, area, here, "cover-letter", library.Skills().Filename, "---\ndescription: A skill.\n---\n")

	names, err := registry.Tree(t.Context(), here, "cover-letter")
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if !slices.Contains(names, library.Skills().Filename) {
		t.Fatalf("Tree = %v, want everything in the directory", names)
	}

	bundle, err := registry.Bundle(t.Context(), here, "cover-letter")
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if len(bundle.Files) != 1 || bundle.Files[0].Path != library.Skills().Filename {
		t.Fatalf("Bundle files = %+v, want the other kind's file among them", bundle.Files)
	}
	if bundle.Document.Description != "What a cover letter is made of." {
		t.Fatalf("Bundle document = %+v, want the blueprint and not the skill", bundle.Document)
	}
}

// A kind that cannot name its own file describes a document the registry would
// look for in a place List never walks, which is startup-invalid config rather
// than a search that quietly finds nothing.
func TestAKindThatCannotNameItsFileIsRefused(t *testing.T) {
	table := vfs.NewMountTable()
	for _, kind := range []library.Kind{
		{},
		{Name: "checklist"},
		{Filename: "CHECKLIST.md"},
		{Name: "checklist", Filename: "docs/CHECKLIST.md"},
		{Name: "checklist", Filename: ".."},
	} {
		if _, err := library.NewRegistry(table, kind, "/checklists", termmatch.New()); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("NewRegistry(%+v) = %v, want fs.ErrInvalid", kind, err)
		}
	}
}
