package config

import "github.com/tunedev/nerve/internal/core/llm"

// Provider is one line of the providers array: what to call a model endpoint,
// which kind builds it, and what that kind needs to know.
//
// The array is the order the gateway asks in, so putting a hosted model in
// front of the laptop's is moving a line. It comes from the file layer alone,
// for the reason a mount does: a chain is a list, and neither an environment
// variable nor a flag carries one.
type Provider struct {
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Settings map[string]string `json:"settings"`
}

// ProviderSpecs is the provider config in the gateway's vocabulary, which is
// what a chain is built from. The JSON spelling stops here.
func (c Config) ProviderSpecs() []llm.ProviderSpec {
	specs := make([]llm.ProviderSpec, len(c.Providers))
	for i, p := range c.Providers {
		specs[i] = llm.ProviderSpec{Name: p.Name, Kind: p.Kind, Settings: p.Settings}
	}
	return specs
}
