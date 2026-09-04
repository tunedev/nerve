package cuefact

import (
	"github.com/tunedev/nerve/internal/core/llm"
)

// Kind is the name a provider config asks for this endpoint by.
const Kind = "cuefact"

// RoleSetting names whose turns this endpoint reads. It is the one thing a
// config can say to it.
const RoleSetting = "role"

// Provider presents the cue extractor as a model endpoint, so the one path a
// fact takes is the same whether it came off a model or out of the sentence the
// user wrote.
//
// It serves extraction and nothing else, which is what the embedded default
// says for it: an endpoint that matches phrases summarises nothing, and a chain
// asked for a summary passes over it.
type Provider struct {
	llm.NoAccessors
	extractor Extractor
}

// AsFactExtractor returns the extractor this endpoint is.
func (p Provider) AsFactExtractor() llm.FactExtractor { return p.extractor }

// Factory builds the provider. A config that names no role gets the one a
// session's caller writes for the person.
func Factory(settings map[string]string) (llm.Provider, error) {
	role := settings[RoleSetting]
	if role == "" {
		role = Role
	}
	return Provider{extractor: New(role)}, nil
}
