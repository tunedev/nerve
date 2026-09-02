package config_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/config"
	"github.com/tunedev/nerve/internal/core/llm"
)

// speaking is a provider that answers with the name its settings gave it, so a
// test can see which endpoint in the chain a call reached. It serves
// summarisation and nothing else, which is what NoAccessors is for.
type speaking struct {
	llm.NoAccessors
	name string
}

func (s speaking) AsSummariser() llm.Summariser { return s }

func (s speaking) Summarise(context.Context, string, int) (string, error) { return s.name, nil }

// The acceptance criterion end to end: a chain that exists only because a
// config document names it, called through the gateway, with nothing in this
// test naming a concrete provider and no caller holding one.
func TestAProviderChainAppearsFromAConfigFile(t *testing.T) {
	cfg, err := config.Load(config.Sources{File: writeFile(t, `{
		"providers": [
			{"name": "hosted", "kind": "speaking", "settings": {"name": "hosted"}},
			{"name": "laptop", "kind": "speaking", "settings": {"name": "laptop"}}
		]
	}`)})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	registry := llm.NewRegistry(cfg.ProviderTimeoutDuration(), cfg.RetryPolicy(), cfg.BreakerPolicy())
	if err := registry.Register("speaking", func(settings map[string]string) (llm.Provider, error) {
		return speaking{name: settings["name"]}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	gateway, err := registry.Gateway(cfg.ProviderSpecs())
	if err != nil {
		t.Fatalf("build gateway: %v", err)
	}

	got, err := gateway.AsSummariser().Summarise(context.Background(), "a document", 1)
	if err != nil {
		t.Fatalf("summarise: %v", err)
	}
	if got != "hosted" {
		t.Errorf("the call reached %q, want %q: the array is the order it is asked", got, "hosted")
	}
}

func TestAConfigWithNoProvidersResolvesNone(t *testing.T) {
	cfg, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if specs := cfg.ProviderSpecs(); len(specs) != 0 {
		t.Errorf("defaults resolved %d providers, want none", len(specs))
	}
}

func TestAMisspelledProviderKeyIsRefused(t *testing.T) {
	_, err := config.Load(config.Sources{File: writeFile(t, `{
		"providers": [{"name": "laptop", "kind": "speaking", "setting": {"name": "laptop"}}]
	}`)})
	if err == nil {
		t.Fatal("a misspelled provider key was accepted")
	}
}

// A provider setting the kind did not declare reads back redacted, which is
// what keeps an API key written beside a model name out of the answer.
func TestEffectiveRedactsAnUndeclaredProviderSetting(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers = []config.Provider{{
		Name:     "hosted",
		Kind:     "remote",
		Settings: map[string]string{"model": "llama3", "api-key": "s3cret"},
	}}

	providers := cfg.Effective(nil, declaring("remote", "model")).Providers
	if len(providers) != 1 {
		t.Fatalf("Providers has %d entries, want 1", len(providers))
	}
	if got := providers[0].Settings["model"]; got != "llama3" {
		t.Errorf("model = %q, want %q", got, "llama3")
	}
	if got := providers[0].Settings["api-key"]; strings.Contains(got, "s3cret") {
		t.Errorf("api-key = %q, still carries the key", got)
	}
}

// The two tables are read with two declarations. A provider kind sharing a name
// with a storage kind must not inherit what the other one declared safe.
func TestAProviderKindDoesNotInheritAStorageKindsDeclaration(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers = []config.Provider{{Name: "hosted", Kind: "s3", Settings: map[string]string{"bucket": "s3cret"}}}

	providers := cfg.Effective(declaring("s3", "bucket"), nil).Providers
	if got := providers[0].Settings["bucket"]; got == "s3cret" {
		t.Errorf("bucket = %q, want it redacted: the storage table declared it, not the provider table", got)
	}
}
