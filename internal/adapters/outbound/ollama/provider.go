package ollama

import (
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// Kind is the name a provider config asks for this endpoint by.
const Kind = "ollama"

// The settings this kind takes: where Ollama is, and which model it is asked
// to embed with.
const (
	BaseSetting  = "base"
	ModelSetting = "model"
)

// DefaultBase is where Ollama listens when a config names no endpoint, which is
// where it listens on the laptop it was installed on.
const DefaultBase = "http://localhost:11434"

// Public names the settings this kind reads back to an operator. Both are
// operational rather than secret: which endpoint a provider points at, and
// which model it embeds with. Ollama holds no credential, so there is none here
// to redact.
var Public = []string{BaseSetting, ModelSetting}

// Provider presents an Ollama endpoint as one link in the gateway's chain.
//
// It serves embeddings and nothing else, which is what the embedded default
// says for it: a chain asked to summarise passes over this endpoint and reaches
// the one that does.
type Provider struct {
	llm.NoAccessors
	embedder *Embedder
}

// AsEmbedder returns the embedding verb this endpoint serves.
func (p Provider) AsEmbedder() llm.Embedder { return p.embedder }

// Factory builds one provider over the endpoint an operator names. The model is
// required and the endpoint is not: there is one obvious address for Ollama and
// no obvious model to ask it for.
//
// It shares no client between providers: connection pooling is per endpoint,
// and one provider's slow host should not be holding another's connections.
func Factory(settings map[string]string) (llm.Provider, error) {
	model, named := settings[ModelSetting]
	if !named {
		return nil, fmt.Errorf("kind %q needs a %q setting naming an embedding model: %w", Kind, ModelSetting, fs.ErrInvalid)
	}

	base := DefaultBase
	if spelled, pointed := settings[BaseSetting]; pointed {
		base = spelled
	}

	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return slices.Contains(Public, key) }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %s, and was given %s: %w", Kind, strings.Join(Public, ", "), strings.Join(extra, ", "), fs.ErrInvalid)
	}

	embedder, err := New(base, model, &http.Client{})
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", Kind, err)
	}
	return Provider{embedder: embedder}, nil
}
