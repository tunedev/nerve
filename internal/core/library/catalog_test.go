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

// The libraries a config names. Two of them are the kinds the package writes
// down; the third is a kind nobody has written a line of Go for, which is what
// the catalog exists to make possible.
var specs = []library.Spec{
	{Kind: library.Skills(), Root: "/skills"},
	{Kind: library.Blueprints(), Root: "/blueprints"},
	{Kind: library.Kind{Name: "checklist", Filename: "CHECKLIST.md"}, Root: "/checklists"},
}

// newCatalog returns a catalog of specs over a mount for each of their roots.
func newCatalog(t *testing.T, specs []library.Spec) *library.Catalog {
	t.Helper()

	table := vfs.NewMountTable()
	for _, spec := range specs {
		if err := table.Mount(spec.Root, memfs.New()); err != nil {
			t.Fatalf("Mount(%q): %v", spec.Root, err)
		}
	}
	catalog, err := library.NewCatalog(table, termmatch.New(), specs)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return catalog
}

// The acceptance: a third document kind is a line of config. The catalog is
// built from what an operator wrote, and the library it hands back for a kind
// this package has never heard of answers the same verbs as the two it names.
func TestCatalogServesAKindNoCodeNames(t *testing.T) {
	catalog := newCatalog(t, specs)

	checklists, err := catalog.Open("checklist")
	if err != nil {
		t.Fatalf("Open(checklist): %v", err)
	}
	mustCreate(t, checklists, here, "interview-prep", "Everything to do before an interview.", "Read the advert.\n")

	got, err := checklists.Get(t.Context(), here, "interview-prep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "Everything to do before an interview." {
		t.Fatalf("Get = %+v, want the document that was created", got)
	}

	matches, err := checklists.Search(t.Context(), here, "prepare for an interview", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0].Card.Name != "interview-prep" {
		t.Fatalf("Search = %+v, want the checklist for the task", matches)
	}
}

// A library holds one kind, so a kind names one library. Two under one name
// would make which library a caller gets depend on the order the config was
// written in.
func TestCatalogRefusesTwoLibrariesOfOneKind(t *testing.T) {
	twice := []library.Spec{
		{Kind: library.Skills(), Root: "/skills"},
		{Kind: library.Skills(), Root: "/elsewhere"},
	}
	if _, err := library.NewCatalog(vfs.NewMountTable(), termmatch.New(), twice); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("NewCatalog(two skill libraries) = %v, want fs.ErrExist", err)
	}
}

// A library the config describes wrongly is a boot failure, not a library that
// answers nothing. The rules are the registry's own, so the catalog carries no
// second copy of them.
func TestCatalogRefusesALibraryItCannotBuild(t *testing.T) {
	for _, spec := range []library.Spec{
		{Kind: library.Kind{Name: "checklist", Filename: "docs/CHECKLIST.md"}, Root: "/checklists"},
		{Kind: library.Kind{Filename: "CHECKLIST.md"}, Root: "/checklists"},
		{Kind: library.Skills(), Root: "skills"},
	} {
		if _, err := library.NewCatalog(vfs.NewMountTable(), termmatch.New(), []library.Spec{spec}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("NewCatalog(%+v) = %v, want fs.ErrInvalid", spec, err)
		}
	}
}

// A kind the config did not name has no library, and the answer says which
// kinds this process was configured with rather than leaving an operator to
// guess at a misspelling.
func TestCatalogHasNoLibraryOfAKindNobodyConfigured(t *testing.T) {
	catalog := newCatalog(t, specs)

	_, err := catalog.Open("recipe")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open(recipe) = %v, want fs.ErrNotExist", err)
	}
	if !slices.Equal(kindNames(catalog.Specs()), []string{"blueprint", "checklist", "skill"}) {
		t.Fatalf("Specs() = %+v, want the three the config named, sorted", catalog.Specs())
	}
}

// kindNames is what the catalog holds, named.
func kindNames(specs []library.Spec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Kind.Name
	}
	return names
}
