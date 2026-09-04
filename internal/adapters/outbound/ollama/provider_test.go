package ollama_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/ollama"
)

// A config that names an embedding model gets an endpoint that embeds and does
// nothing else, so a chain asked to summarise or to write SQL passes over it and
// reaches the one that does.
func TestTheEndpointServesWhatItsConfigNamedAModelFor(t *testing.T) {
	provider, err := ollama.Factory(map[string]string{ollama.ModelSetting: "nomic-embed-text"})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	if provider.AsEmbedder() == nil {
		t.Error("AsEmbedder = nil, want the endpoint's embedding verb")
	}
	if verb := provider.AsSQLWriter(); verb != nil {
		t.Errorf("AsSQLWriter = %v, want nil: this config named no model to write with", verb)
	}
	if verb := provider.AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil: this endpoint writes no sentences", verb)
	}
}

// A config that names a model to write SQL with gets an endpoint that writes
// SQL, whether or not it embeds. One Ollama commonly runs both models, and a
// config naming one of them should not have to name the other.
func TestAnEndpointNamedForSQLWritesSQL(t *testing.T) {
	writing, err := ollama.Factory(map[string]string{ollama.SQLModelSetting: "qwen2.5-coder"})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}
	if writing.AsSQLWriter() == nil {
		t.Error("AsSQLWriter = nil, want the endpoint's SQL-writing verb")
	}
	if verb := writing.AsEmbedder(); verb != nil {
		t.Errorf("AsEmbedder = %v, want nil: this config named no model to embed with", verb)
	}

	both, err := ollama.Factory(map[string]string{
		ollama.ModelSetting:    "nomic-embed-text",
		ollama.SQLModelSetting: "qwen2.5-coder",
	})
	if err != nil {
		t.Fatalf("Factory naming both models = %v", err)
	}
	if both.AsEmbedder() == nil || both.AsSQLWriter() == nil {
		t.Error("an endpoint named for both models serves one of them, want both")
	}
}

// A config that names no model at all is refused at boot. There is no model to
// guess, an index built with the wrong one is comparable with nothing, and a
// link in the chain that serves nothing is a config nobody meant to write.
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

// Every setting is operational, so every one reads back to an operator. Ollama
// holds no credential, and a kind that declared one of these secret would be
// hiding which model an index was built with.
func TestEverySettingReadsBackToAnOperator(t *testing.T) {
	want := []string{ollama.BaseSetting, ollama.ModelSetting, ollama.SQLModelSetting}
	if !slices.Equal(ollama.Public, want) {
		t.Fatalf("Public = %v, want %v", ollama.Public, want)
	}
}
