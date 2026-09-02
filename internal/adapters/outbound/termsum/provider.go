package termsum

import "github.com/tunedev/nerve/internal/core/llm"

// Kind is the name a provider config asks for this endpoint by.
const Kind = "termsum"

// Provider presents the extractive summariser as a model endpoint, so the one
// path a summary takes is the same whether the sentences came off a model or
// out of the document.
//
// It serves summarisation and nothing else, which is what the embedded default
// says for it: an endpoint that picks sentences by term weight embeds nothing
// and extracts nothing, and a chain asked for either passes over it.
type Provider struct {
	llm.NoAccessors
	summariser Summariser
}

// AsSummariser returns the summariser this endpoint is.
func (p Provider) AsSummariser() llm.Summariser { return p.summariser }

// Factory builds the provider. It reads no settings: the weights come from the
// document it is handed, so there is nothing to point it at and nothing to
// tune.
func Factory(map[string]string) (llm.Provider, error) { return Provider{summariser: New()}, nil }
