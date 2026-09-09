package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tunedev/nerve/internal/core/stability"
)

// Gateway is an ordered chain of named providers that is itself a Provider, the
// way the mount table is itself a FileSystem. A caller holds one and cannot
// tell how many endpoints stand behind it.
//
// Order is the routing rule and the whole of it. There is no second table
// saying which provider serves which capability: a capability is served by the
// providers that answer its accessor, in the order config listed them, and the
// first one to answer is the answer. That is what makes adding an endpoint, or
// putting a hosted one in front of the laptop's, a line of config.
type Gateway struct {
	links []*link
}

// link is one provider with the stability policy that belongs to it: its own
// deadline per attempt, its own circuit, and the retry policy the process runs
// under.
//
// The circuit is per provider because a circuit shared between two would take a
// working endpoint down with a broken one, which is the failure the chain
// exists to prevent.
type link struct {
	name     string
	provider Provider
	timeout  time.Duration
	retry    stability.Retry
	circuit  *stability.Circuit
}

// call makes one call to this provider, bounded, guarded and retried.
//
// The three go on in the order the mount registry documents, and it is the
// order that makes them work together. The bound is innermost, so every attempt
// is a call with its own deadline rather than the attempts sharing one that the
// first hang spends. The circuit is next, so it counts attempts rather than
// sequences and the retry above it stops the moment it opens. The retry is
// outermost, so what it repeats is a bounded, guarded call.
func (l *link) call(ctx context.Context, run func(context.Context) error) error {
	return l.retry.Do(ctx, func(ctx context.Context) error {
		return l.circuit.Guard(func() error {
			bounded, cancel := context.WithTimeout(ctx, l.timeout)
			defer cancel()
			return run(bounded)
		})
	})
}

// Providers names the chain in the order it is asked, which is what a banner
// and an operator surface read back.
func (g *Gateway) Providers() []string {
	names := make([]string, len(g.links))
	for i, l := range g.links {
		names[i] = l.name
	}
	return names
}

// Capabilities names what the chain serves, in the order this package declares
// its accessors. It is what a banner and an operator surface read back: a build
// with endpoints configured and nothing between them that serves the capability
// a stage wants looks wired and answers nothing.
func (g *Gateway) Capabilities() []string {
	var served []string
	if g.AsSummariser() != nil {
		served = append(served, "summarise")
	}
	if g.AsEmbedder() != nil {
		served = append(served, "embed")
	}
	if g.AsFactExtractor() != nil {
		served = append(served, "extract")
	}
	if g.AsFactConsolidator() != nil {
		served = append(served, "consolidate")
	}
	if g.AsSQLWriter() != nil {
		served = append(served, "write-sql")
	}
	if g.AsEntityExtractor() != nil {
		served = append(served, "entities")
	}
	return served
}

// AsSummariser returns a Summariser routed across every provider that has one,
// or nil when no provider in the chain summarises.
//
// The nil is the same answer a provider gives, because the gateway is a
// provider: a caller that guards for nil keeps working when the chain is a
// chain rather than an endpoint, and a build configured with no summarising
// endpoint finds that out where it wires the pipeline rather than on the first
// document.
func (g *Gateway) AsSummariser() Summariser {
	chain := serving(g, Provider.AsSummariser)
	if len(chain) == 0 {
		return nil
	}
	return summarising{chain: chain}
}

// summarising is the routed summarisation verb over the providers that serve
// it, resolved once when the accessor answered rather than per call.
type summarising struct{ chain []served[Summariser] }

func (s summarising) Summarise(ctx context.Context, text string, sentences int) (string, error) {
	return route(ctx, s.chain, func(ctx context.Context, provider Summariser) (string, error) {
		return provider.Summarise(ctx, text, sentences)
	})
}

// AsEmbedder returns an Embedder routed across every provider that has one, or
// nil when no provider in the chain embeds.
//
// The nil is what a build with no embedding endpoint reads back where it wires
// an index, rather than on the first document it tries to index.
func (g *Gateway) AsEmbedder() Embedder {
	chain := serving(g, Provider.AsEmbedder)
	if len(chain) == 0 {
		return nil
	}
	return embedding{chain: chain}
}

// embedding is the routed embedding verb over the providers that serve it,
// resolved once when the accessor answered rather than per call.
type embedding struct{ chain []served[Embedder] }

func (e embedding) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return route(ctx, e.chain, func(ctx context.Context, provider Embedder) ([][]float32, error) {
		return provider.Embed(ctx, texts)
	})
}

// AsFactExtractor returns a FactExtractor routed across every provider that has
// one, or nil when no provider in the chain extracts. It is nil for the reason
// AsSummariser is: a build with no extracting endpoint finds that out where it
// wires the pipeline rather than on the first transcript it commits.
func (g *Gateway) AsFactExtractor() FactExtractor {
	chain := serving(g, Provider.AsFactExtractor)
	if len(chain) == 0 {
		return nil
	}
	return extracting{chain: chain}
}

// extracting is the routed extraction verb over the providers that serve it,
// resolved once when the accessor answered rather than per call.
type extracting struct{ chain []served[FactExtractor] }

func (e extracting) Extract(ctx context.Context, turns []Turn) ([]Fact, error) {
	return route(ctx, e.chain, func(ctx context.Context, provider FactExtractor) ([]Fact, error) {
		return provider.Extract(ctx, turns)
	})
}

// AsFactConsolidator returns a FactConsolidator routed across every provider
// that has one, or nil when no provider in the chain merges facts. It is nil
// for the reason AsFactExtractor is: a build with no consolidating endpoint
// finds that out where it wires the pipeline rather than on the first
// transcript it commits.
func (g *Gateway) AsFactConsolidator() FactConsolidator {
	chain := serving(g, Provider.AsFactConsolidator)
	if len(chain) == 0 {
		return nil
	}
	return consolidating{chain: chain}
}

// consolidating is the routed consolidation verb over the providers that serve
// it, resolved once when the accessor answered rather than per call.
type consolidating struct{ chain []served[FactConsolidator] }

func (c consolidating) Consolidate(ctx context.Context, facts []Fact) ([]Narrative, error) {
	return route(ctx, c.chain, func(ctx context.Context, provider FactConsolidator) ([]Narrative, error) {
		return provider.Consolidate(ctx, facts)
	})
}

// AsSQLWriter returns an SQLWriter routed across every provider that has one,
// or nil when no provider in the chain writes SQL. It is nil for the reason
// AsSummariser is: a build with no endpoint that writes SQL finds that out
// where it wires the query path rather than on the first question asked.
func (g *Gateway) AsSQLWriter() SQLWriter {
	chain := serving(g, Provider.AsSQLWriter)
	if len(chain) == 0 {
		return nil
	}
	return writing{chain: chain}
}

// writing is the routed SQL-writing verb over the providers that serve it,
// resolved once when the accessor answered rather than per call.
type writing struct{ chain []served[SQLWriter] }

func (w writing) WriteSQL(ctx context.Context, question string, table Table) (string, error) {
	return route(ctx, w.chain, func(ctx context.Context, provider SQLWriter) (string, error) {
		return provider.WriteSQL(ctx, question, table)
	})
}

// AsEntityExtractor returns an EntityExtractor routed across every provider
// that has one, or nil when no provider in the chain reads entities. It is nil
// for the reason AsFactExtractor is: a build with no endpoint that reads
// entities finds that out where it wires the graph rather than on the first
// document it indexes.
func (g *Gateway) AsEntityExtractor() EntityExtractor {
	chain := serving(g, Provider.AsEntityExtractor)
	if len(chain) == 0 {
		return nil
	}
	return reading{chain: chain}
}

// reading is the routed entity extraction verb over the providers that serve
// it, resolved once when the accessor answered rather than per call.
type reading struct{ chain []served[EntityExtractor] }

func (r reading) ExtractEntities(ctx context.Context, text string) (Extraction, error) {
	return route(ctx, r.chain, func(ctx context.Context, provider EntityExtractor) (Extraction, error) {
		return provider.ExtractEntities(ctx, text)
	})
}

// served is one provider that answers a given accessor, paired with the verb it
// answered. Resolving the accessor once is what makes a routed call a walk over
// endpoints that serve the capability rather than over every endpoint there is.
type served[T any] struct {
	link *link
	verb T
}

// serving resolves an accessor across the chain, keeping the providers that
// answer it in the order config listed them.
func serving[T any](g *Gateway, pick func(Provider) T) []served[T] {
	var chain []served[T]
	for _, l := range g.links {
		if verb := pick(l.provider); any(verb) != nil {
			chain = append(chain, served[T]{link: l, verb: verb})
		}
	}
	return chain
}

// route asks each provider in the chain, in order, and answers with the first
// one that answers.
//
// Which failures move on to the next provider is the only judgement here, and
// it is Classify's. A transient failure the attempts could not outlast, and a
// circuit already open, are the endpoint failing rather than the call being
// wrong, so the next endpoint is asked. Anything else is an answer: a request
// one model refused is refused by the next model too, and asking again is a
// slower failure that costs a second dependency on the way.
//
// A caller that has gone gets no further provider. What it sees is the last
// real failure rather than a cancellation, since that is the one worth reading.
//
// When every provider has failed, the last failure comes back named by the
// provider that gave it. The chain is never empty: an accessor that found no
// provider serving the capability answered nil rather than a routed verb.
func route[T, R any](ctx context.Context, chain []served[T], call func(context.Context, T) (R, error)) (R, error) {
	var zero R

	var last error
	for _, s := range chain {
		var answer R
		err := s.link.call(ctx, func(ctx context.Context) error {
			var err error
			answer, err = call(ctx, s.verb)
			return err
		})
		if err == nil {
			return answer, nil
		}

		last = fmt.Errorf("provider %q: %w", s.link.name, err)
		if !degraded(err) || ctx.Err() != nil {
			return zero, last
		}
	}
	return zero, last
}

// degraded reports whether the failure was the endpoint's rather than the
// call's, which is the one case worth asking a second endpoint.
func degraded(err error) bool {
	return stability.Classify(err) == stability.Retryable || errors.Is(err, stability.ErrUnavailable)
}
