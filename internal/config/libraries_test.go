package config_test

import (
	"testing"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/library"
)

// A library exists because a config document names it, and what arrives at the
// catalog is the kind and the root in the domain's own vocabulary.
func TestALibraryAppearsFromAConfigFile(t *testing.T) {
	cfg, err := config.Load(config.Sources{File: writeFile(t, `{
		"libraries": [
			{"kind": "skill",     "file": "SKILL.md",     "root": "/skills"},
			{"kind": "checklist", "file": "CHECKLIST.md", "root": "/checklists"}
		]
	}`)})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	specs := cfg.LibrarySpecs()
	want := []library.Spec{
		{Kind: library.Skills(), Root: "/skills"},
		{Kind: library.Kind{Name: "checklist", Filename: "CHECKLIST.md"}, Root: "/checklists"},
	}
	if len(specs) != len(want) {
		t.Fatalf("LibrarySpecs() = %+v, want %+v", specs, want)
	}
	for i, spec := range specs {
		if spec != want[i] {
			t.Errorf("LibrarySpecs()[%d] = %+v, want %+v", i, spec, want[i])
		}
	}
}

func TestAConfigWithNoLibrariesResolvesNone(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if specs := cfg.LibrarySpecs(); len(specs) != 0 {
		t.Errorf("defaults resolved %d libraries, want none", len(specs))
	}
}

func TestAMisspelledLibraryKeyIsRefused(t *testing.T) {
	_, err := config.Load(config.Sources{File: writeFile(t, `{
		"libraries": [{"kind": "skill", "filename": "SKILL.md", "root": "/skills"}]
	}`)})
	if err == nil {
		t.Fatal("a misspelled library key was accepted")
	}
}

// The operator surface answers with the library table, because a kind that
// exists only in config is a kind nobody can otherwise read back.
func TestEffectiveEchoesTheLibraryTable(t *testing.T) {
	cfg, err := config.Load(config.Sources{File: writeFile(t, `{
		"libraries": [{"kind": "checklist", "file": "CHECKLIST.md", "root": "/checklists"}]
	}`)})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	libraries := cfg.Effective(nil, nil).Libraries
	if len(libraries) != 1 || libraries[0] != (config.Library{Kind: "checklist", File: "CHECKLIST.md", Root: "/checklists"}) {
		t.Errorf("Effective().Libraries = %+v, want the library the config named", libraries)
	}
}
