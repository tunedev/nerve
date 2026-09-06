package nounlink

import "github.com/tunedev/nerve/internal/core/llm"

// Kind is the name a provider config asks for this endpoint by.
const Kind = "nounlink"

// Provider presents the capital-reading extractor as a model endpoint, so the
// one path an entity takes is the same whether it came off a model or out of
// the shape of the sentence.
//
// It serves entity extraction and nothing else, which is what the embedded
// default says for it: an endpoint that reads capitals summarises nothing and
// remembers nothing, and a chain asked for either passes over it.
type Provider struct {
	llm.NoAccessors
	extractor Extractor
}

// AsEntityExtractor returns the extractor this endpoint is.
func (p Provider) AsEntityExtractor() llm.EntityExtractor { return p.extractor }

// Factory builds the provider. It reads no settings: the rules come from the
// sentence it is handed, so there is nothing to point it at and nothing to
// tune.
func Factory(map[string]string) (llm.Provider, error) { return Provider{extractor: New()}, nil }
