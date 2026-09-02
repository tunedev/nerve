package llm_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/core/llm"
)

// stub is the factory a registry test registers when what it is testing is the
// registry rather than the provider.
func stub(map[string]string) (llm.Provider, error) { return &endpoint{}, nil }

// registry is one holding the kind name given, under the policies the gateway
// tests share.
func registry(t *testing.T, kinds ...string) *llm.Registry {
	t.Helper()

	r := llm.NewRegistry(patience, once, prompt)
	for _, kind := range kinds {
		if err := r.Register(kind, stub); err != nil {
			t.Fatalf("Register(%q) = %v", kind, err)
		}
	}
	return r
}

// An endpoint appears by naming a registered kind in config. Nothing in the
// core knows a concrete provider, which is what makes swapping the laptop's
// summariser for a hosted model a line of a document rather than an edit.
func TestAProviderIsBuiltFromConfig(t *testing.T) {
	gateway, err := registry(t, "ollama").Gateway([]llm.ProviderSpec{{Name: "local", Kind: "ollama"}})
	if err != nil {
		t.Fatalf("Gateway = %v", err)
	}

	if names := gateway.Providers(); len(names) != 1 || names[0] != "local" {
		t.Errorf("Providers = %v, want the configured name", names)
	}
}

// A misspelled kind stops the boot naming the kinds the build has. Booting
// without an endpoint an operator asked for is a fallback that is not there on
// the night it is needed.
func TestAnUnregisteredKindStopsTheBoot(t *testing.T) {
	_, err := registry(t, "ollama").Gateway([]llm.ProviderSpec{{Name: "local", Kind: "olama"}})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Gateway = %v, want %v", err, fs.ErrNotExist)
	}
	if !strings.Contains(err.Error(), "ollama") {
		t.Errorf("Gateway = %v, want the registered kinds named", err)
	}
}

// Two endpoints under one name stop the boot. A name is how a failure says
// which endpoint failed, and two of them make the answer a guess.
func TestTwoProvidersUnderOneNameStopTheBoot(t *testing.T) {
	specs := []llm.ProviderSpec{{Name: "local", Kind: "ollama"}, {Name: "local", Kind: "ollama"}}

	_, err := registry(t, "ollama").Gateway(specs)
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("Gateway = %v, want %v", err, fs.ErrExist)
	}
}

// Two factories under one kind name make which provider a config gets depend on
// registration order, so the second refuses.
func TestAKindIsRegisteredOnce(t *testing.T) {
	if err := registry(t, "ollama").Register("ollama", stub); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Register = %v, want %v", err, fs.ErrExist)
	}
}

// A kind names the settings it is willing to read back. What it does not name
// is a credential as far as anyone else is concerned, which is how an API key
// beside a model name stays out of the operator surface.
func TestAKindDeclaresWhatIsSafeToReadBack(t *testing.T) {
	r := llm.NewRegistry(patience, once, prompt)
	if err := r.Register("ollama", stub, "model"); err != nil {
		t.Fatalf("Register = %v", err)
	}

	if public := r.Public("ollama"); len(public) != 1 || public[0] != "model" {
		t.Errorf("Public = %v, want the declared key", public)
	}
	if public := r.Public("nobody registered this"); len(public) != 0 {
		t.Errorf("Public of an unregistered kind = %v, want none", public)
	}
}

// A factory that cannot build stops the boot naming the endpoint and its kind,
// because a settings mistake is found once at boot or once per call forever.
func TestAFactoryFailureStopsTheBoot(t *testing.T) {
	r := llm.NewRegistry(patience, once, prompt)
	if err := r.Register("ollama", func(map[string]string) (llm.Provider, error) {
		return nil, errors.New("model is not pulled")
	}); err != nil {
		t.Fatalf("Register = %v", err)
	}

	_, err := r.Gateway([]llm.ProviderSpec{{Name: "local", Kind: "ollama"}})
	if err == nil || !strings.Contains(err.Error(), `provider "local" of kind "ollama"`) {
		t.Errorf("Gateway = %v, want the endpoint and its kind named", err)
	}
}
