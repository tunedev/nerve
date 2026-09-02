// Package llm is the gateway: the one path a model call in this process takes.
//
// A model is a remote thing that fails the way remote things fail, and there is
// more than one of it — Ollama on the laptop, a hosted endpoint when there is
// one, a stand-in that needs no network at all. So nothing else holds a client.
// A stage that wants a summary holds a Summariser this package routes, the way
// a caller of the tree holds the mount table rather than a plugin, and which
// endpoint answers is a line of config.
//
// That is what the ordered chain buys. One flaky endpoint degrades a feature
// instead of taking down the path that used it: the provider that cannot answer
// is passed over, the next one is asked, and a caller learns only that it has a
// summary.
//
// Capability is reached through accessors and never through a type assertion.
// Every caller holds the Gateway rather than a provider, so gateway.(Embedder)
// would interrogate the router and never the thing that would do the work —
// the same defect that put ResponseController in the standard library. An
// accessor on the port forces the router to forward.
package llm

import "context"

// Provider is one model endpoint, described by what it will answer.
//
// It has no verbs of its own. Everything a provider does is reached through an
// accessor that returns a narrow interface or nil, because no endpoint serves
// every capability: the laptop's extractive summariser embeds nothing, and an
// embedding model writes no sentences. A provider that says nothing is legal
// and useless, which is the right shape for a port whose implementations differ
// in what they are for.
type Provider interface {
	// AsSummariser returns the endpoint's summarisation verb, or nil when it
	// does not summarise.
	AsSummariser() Summariser
}

// NoAccessors is the default a provider embeds so that implementing one
// capability costs one method. Every accessor answers nil, and a provider
// overrides the ones it serves.
type NoAccessors struct{}

// AsSummariser reports that the provider does not summarise.
func (NoAccessors) AsSummariser() Summariser { return nil }

// Summariser says what a document is about in at most the sentences it is
// given.
//
// The budget is a ceiling and not a quota: a document with less to say than the
// budget allows says less. What separates two summarisers is whether the
// sentences are the document's own, and the caller is not told which it has.
type Summariser interface {
	Summarise(ctx context.Context, text string, sentences int) (string, error)
}
