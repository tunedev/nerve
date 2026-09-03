package ollama_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
)

// The provider is an embedding endpoint and nothing else, so a chain asked to
// summarise passes over it and reaches the one that does.
func TestTheProviderServesEmbeddingAlone(t *testing.T) {
	provider, err := ollama.Factory(map[string]string{ollama.ModelSetting: "nomic-embed-text"})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	if provider.AsEmbedder() == nil {
		t.Error("AsEmbedder = nil, want the endpoint's embedding verb")
	}
	if verb := provider.AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil: this endpoint writes no sentences", verb)
	}
}

// A config that names no model is refused at boot. There is no model to guess
// and an index built with the wrong one is comparable with nothing.
func TestAConfigWithNoModelIsRefused(t *testing.T) {
	if _, err := ollama.Factory(nil); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(nil) = %v, want %v", err, fs.ErrInvalid)
	}
}

// A setting the kind does not take is a misspelling, and a misspelled setting
// that is quietly ignored is config that reads as though it did something.
func TestASettingTheKindDoesNotTakeIsRefused(t *testing.T) {
	settings := map[string]string{ollama.ModelSetting: "nomic-embed-text", "dimensions": "768"}
	if _, err := ollama.Factory(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(%v) = %v, want %v", settings, err, fs.ErrInvalid)
	}
}

// A config that names no endpoint gets the one Ollama listens on, which is the
// laptop it was installed on.
func TestAConfigWithNoEndpointReachesTheLaptop(t *testing.T) {
	if _, err := ollama.Factory(map[string]string{ollama.ModelSetting: "nomic-embed-text"}); err != nil {
		t.Errorf("Factory with no %q = %v, want the default endpoint", ollama.BaseSetting, err)
	}
}

// A misspelled endpoint fails where the provider is built, so the process does
// not boot on a chain whose second endpoint is a typo.
func TestAMisspelledEndpointIsRefusedAtBuild(t *testing.T) {
	settings := map[string]string{ollama.ModelSetting: "nomic-embed-text", ollama.BaseSetting: "localhost:11434"}
	if _, err := ollama.Factory(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(%v) = %v, want %v", settings, err, fs.ErrInvalid)
	}
}

// Both settings are operational, so both read back to an operator. Ollama holds
// no credential, and a kind that declared one of these secret would be hiding
// which model an index was built with.
func TestBothSettingsReadBackToAnOperator(t *testing.T) {
	if len(ollama.Public) != 2 {
		t.Fatalf("Public = %v, want both settings", ollama.Public)
	}
}
