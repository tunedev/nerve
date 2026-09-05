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

// The settings this kind takes: where Ollama is, and which model it is asked to
// embed with and which to write SQL with.
//
// There are two models because they are two models. Nothing embeds a corpus and
// writes SQL well, and a single setting would make a config choose which
// capability to be wrong about.
const (
	BaseSetting     = "base"
	ModelSetting    = "model"
	SQLModelSetting = "sql_model"
)

// DefaultBase is where Ollama listens when a config names no endpoint, which is
// where it listens on the laptop it was installed on.
const DefaultBase = "http://localhost:11434"

// Public names the settings this kind reads back to an operator. All three are
// operational rather than secret: which endpoint a provider points at, and
// which models it calls. Ollama holds no credential, so there is none here to
// redact.
var Public = []string{BaseSetting, ModelSetting, SQLModelSetting}

// Provider presents an Ollama endpoint as one link in the gateway's chain.
//
// It serves the capabilities its config named a model for and no others, which
// is what the embedded default says for the rest: a chain asked to summarise
// passes over this endpoint and reaches the one that does.
type Provider struct {
	llm.NoAccessors
	embedder *Embedder
	writer   *Writer
}

// AsEmbedder returns the embedding verb this endpoint serves, or nil when its
// config named no model to embed with.
//
// The nil is spelled out rather than returned as a typed one, because an
// interface holding a nil pointer is not nil and the chain would route
// embeddings to an endpoint that has none.
func (p Provider) AsEmbedder() llm.Embedder {
	if p.embedder == nil {
		return nil
	}
	return p.embedder
}

// AsSQLWriter returns the SQL-writing verb this endpoint serves, or nil when
// its config named no model to write with.
func (p Provider) AsSQLWriter() llm.SQLWriter {
	if p.writer == nil {
		return nil
	}
	return p.writer
}

// Factory builds one provider over the endpoint an operator names.
//
// A model is required and the endpoint is not: there is one obvious address for
// Ollama and no obvious model to ask it for. Which model is named decides what
// the endpoint serves, and naming none at all is a link in the chain that
// answers nothing.
//
// It shares no client between providers: connection pooling is per endpoint,
// and one provider's slow host should not be holding another's connections.
func Factory(settings map[string]string) (llm.Provider, error) {
	if extra := slices.DeleteFunc(slices.Sorted(maps.Keys(settings)), func(key string) bool { return slices.Contains(Public, key) }); len(extra) > 0 {
		return nil, fmt.Errorf("kind %q takes only %s, and was given %s: %w", Kind, strings.Join(Public, ", "), strings.Join(extra, ", "), fs.ErrInvalid)
	}

	embed, embeds := settings[ModelSetting]
	write, writes := settings[SQLModelSetting]
	if !embeds && !writes {
		return nil, fmt.Errorf("kind %q needs a %q or a %q setting naming a model: %w", Kind, ModelSetting, SQLModelSetting, fs.ErrInvalid)
	}

	base := DefaultBase
	if spelled, pointed := settings[BaseSetting]; pointed {
		base = spelled
	}

	provider := Provider{}
	if embeds {
		embedder, err := New(base, embed, &http.Client{})
		if err != nil {
			return nil, fmt.Errorf("kind %q: %w", Kind, err)
		}
		provider.embedder = embedder
	}
	if writes {
		writer, err := NewWriter(base, write, &http.Client{})
		if err != nil {
			return nil, fmt.Errorf("kind %q: %w", Kind, err)
		}
		provider.writer = writer
	}
	return provider, nil
}
