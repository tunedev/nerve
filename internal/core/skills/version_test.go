package skills_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/localrefs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/adapters/outbound/treecopy"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/skills"
	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// A skill library is versioned by composition and not by verbs on the registry.
// The library is a subtree of a mount, so a versioner over a working copy of
// that subtree commits and restores skills without either package knowing the
// other exists. This is the test that keeps that true: a version verb added to
// the registry would make it redundant, which is the point.
func TestTheLibraryIsVersionedWithoutTheRegistryKnowing(t *testing.T) {
	table := vfs.NewMountTable()
	for _, prefix := range []string{root, "/objects", "/refs"} {
		if err := table.Mount(prefix, memfs.New()); err != nil {
			t.Fatalf("Mount(%q): %v", prefix, err)
		}
	}

	registry, err := skills.NewRegistry(table, root, termmatch.New())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	library, err := here.Path(root, "")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	versioner := version.NewVersioner(
		treecopy.New(table, library, "/refs/state"),
		cas.New(table, "/objects"),
		localrefs.New(table, "/refs/names"),
	)

	mustCreate(t, registry, here, "apply", "Apply to an advert.", "first")
	if _, err := versioner.Commit(t.Context(), "main", "the library as it was"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	mustCreate(t, registry, here, "apply", "Apply to an advert.", "second")
	mustCreate(t, registry, here, "screen", "Score a CV against a role.", "body")

	changed, err := versioner.Status(t.Context(), "main")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("Status found no change after the library was edited")
	}

	id, err := versioner.Commit(t.Context(), "main", "the library as it is")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	versions, err := versioner.ListVersions(t.Context(), "main")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 || versions[0].ID != id {
		t.Fatalf("ListVersions = %+v, want the two commits newest first", versions)
	}

	if _, err := versioner.Restore(t.Context(), versions[1].ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := registry.Get(t.Context(), here, "apply")
	if err != nil {
		t.Fatalf("Get after Restore: %v", err)
	}
	if got.Body != "first" {
		t.Fatalf("Get after Restore = %+v, want the skill as it was committed", got)
	}
	if _, err := registry.Get(t.Context(), here, "screen"); err == nil {
		t.Fatal("Get after Restore found the skill added since the commit")
	}
}
