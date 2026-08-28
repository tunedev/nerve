package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/termmatch"
	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/library"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// wire builds what main builds, from the config document in body: the kinds
// this binary has, the table its mounts make, and the libraries its kinds are
// kept in.
func wire(t *testing.T, body string) *library.Catalog {
	t.Helper()

	path := filepath.Join(t.TempDir(), "nerve.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(config.Sources{File: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	registry := vfs.NewRegistry(cfg.CallTimeoutDuration(), cfg.RetryPolicy(), cfg.BreakerPolicy())
	if err := registerKinds(registry, "test"); err != nil {
		t.Fatalf("registerKinds: %v", err)
	}
	table, err := registry.Table(cfg.MountSpecs())
	if err != nil {
		t.Fatalf("build table: %v", err)
	}

	catalog, err := library.NewCatalog(table, termmatch.New(), cfg.LibrarySpecs())
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	return catalog
}

// The acceptance, end to end: a third document kind is a config document. The
// binary names no checklist, the storage it lands on is whatever the same
// document mounted, and every library verb answers.
func TestAThirdDocumentKindNeedsConfigNotCode(t *testing.T) {
	catalog := wire(t, `{
		"mounts":    [{"prefix": "/checklists", "kind": "memfs"}],
		"libraries": [{"kind": "checklist", "file": "CHECKLIST.md", "root": "/checklists"}]
	}`)

	checklists, err := catalog.Open("checklist")
	if err != nil {
		t.Fatalf("Open(checklist): %v", err)
	}

	here := workspace.Scope{User: "ada", Workspace: "hunt"}
	doc := library.Document{
		Card: library.Card{Name: "interview-prep", Description: "Everything to do before an interview."},
		Body: "Read the advert again.\n",
	}
	if err := checklists.Create(t.Context(), here, doc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	matches, err := checklists.Search(t.Context(), here, "prepare for the interview", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0].Card.Name != "interview-prep" {
		t.Fatalf("Search = %+v, want the checklist for the task", matches)
	}

	got, err := checklists.Get(t.Context(), here, "interview-prep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != doc {
		t.Errorf("Get = %+v, want %+v", got, doc)
	}
}

// A kind's root is where its mount is, so a library the config puts on a
// different kind of storage is the same library on a different disk.
func TestALibraryLandsOnTheStorageItsRootIsMountedOn(t *testing.T) {
	root := t.TempDir()
	catalog := wire(t, `{
		"mounts":    [{"prefix": "/checklists", "kind": "localfs", "settings": {"root": "`+root+`"}}],
		"libraries": [{"kind": "checklist", "file": "CHECKLIST.md", "root": "/checklists"}]
	}`)

	checklists, err := catalog.Open("checklist")
	if err != nil {
		t.Fatalf("Open(checklist): %v", err)
	}
	doc := library.Document{
		Card: library.Card{Name: "interview-prep", Description: "Everything to do before an interview."},
		Body: "Read the advert again.\n",
	}
	if err := checklists.Create(t.Context(), workspace.Scope{User: "ada", Workspace: "hunt"}, doc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "ada", "hunt", "interview-prep", "CHECKLIST.md")); err != nil {
		t.Errorf("stat the document on disk: %v", err)
	}
}

// A library whose root has no mount is not a boot failure. Mounts may arrive
// after the table has been read, so where a library sits is a promise about a
// prefix rather than a claim that something answers for it yet; until something
// does, the tree says there is nothing there.
func TestALibraryWhoseRootIsNotMountedYetFindsNothing(t *testing.T) {
	catalog := wire(t, `{
		"libraries": [{"kind": "checklist", "file": "CHECKLIST.md", "root": "/checklists"}]
	}`)

	checklists, err := catalog.Open("checklist")
	if err != nil {
		t.Fatalf("Open(checklist): %v", err)
	}
	if err := checklists.Create(t.Context(), workspace.Scope{User: "ada", Workspace: "hunt"}, library.Document{
		Card: library.Card{Name: "interview-prep", Description: "Everything to do before an interview."},
	}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Create over no mount = %v, want fs.ErrNotExist", err)
	}
}
