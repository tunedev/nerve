package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/termsum"
	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// providerRegistry is one holding every provider kind this build has, under the
// policies a test does not care about.
func providerRegistry(t *testing.T) *llm.Registry {
	t.Helper()

	registry := llm.NewRegistry(time.Second, vfs.Retry{Attempts: 1}, vfs.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registerProviders(registry); err != nil {
		t.Fatalf("registerProviders() = %v", err)
	}
	return registry
}

// The composition root is the only place that knows a concrete provider, so it
// is the only place a provider kind can be registered — and a kind the config
// can name is the whole point of the registry.
func TestRegisterProvidersMakesTermsumRoutable(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway([]llm.ProviderSpec{{Name: "laptop", Kind: termsum.Kind}})
	if err != nil {
		t.Fatalf("Gateway(laptop) = %v", err)
	}

	if served := gateway.Capabilities(); !slices.Contains(served, "summarise") {
		t.Fatalf("the gateway serves %v, want summarisation", served)
	}

	answer, err := gateway.AsSummariser().Summarise(context.Background(), "The pumps had not run since March. Nobody funded the maintenance.", 1)
	if err != nil {
		t.Fatalf("Summarise = %v", err)
	}
	if answer == "" {
		t.Error("Summarise returned nothing, want a sentence of the document")
	}
}

// termsum reads no settings, so it declares none safe to read back and the
// effective-config surface has nothing of its to show.
func TestTermsumDeclaresNoPublicSettings(t *testing.T) {
	if public := providerRegistry(t).Public(termsum.Kind); len(public) != 0 {
		t.Errorf("Public(%q) = %v, want none", termsum.Kind, public)
	}
}

// A build with no providers configured runs and says it serves nothing, which
// is the laptop before anyone has named an endpoint.
func TestABuildWithNoProvidersServesNothing(t *testing.T) {
	gateway, err := providerRegistry(t).Gateway(nil)
	if err != nil {
		t.Fatalf("Gateway(nil) = %v", err)
	}

	if served := gateway.Capabilities(); len(served) != 0 {
		t.Errorf("the gateway serves %v, want nothing", served)
	}
}
