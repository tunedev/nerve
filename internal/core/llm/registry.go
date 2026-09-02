package llm

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Factory builds one provider from the settings its config names.
//
// Settings are strings for the reason a mount's are: a config file, an
// environment variable and a command line all carry strings, and what a setting
// means is the provider's business, parsed where the provider is built.
type Factory func(settings map[string]string) (Provider, error)

// ProviderSpec is one line of provider config: what to call this endpoint,
// which kind builds it, and what that kind needs to know.
type ProviderSpec struct {
	// Name is what this endpoint is called in an error, a log line and the
	// operator surface. It is separate from Kind because two endpoints of one
	// kind is the case the chain exists for.
	Name string
	// Kind is the registered name of the factory that builds it.
	Kind string
	// Settings are what that factory needs. A kind that needs nothing takes a
	// nil map.
	Settings map[string]string
}

// Registry maps a kind name onto the factory that builds it, so a new endpoint
// is a line of config naming a registered kind rather than an edit to a switch.
//
// Nothing here knows a concrete provider. Kinds are registered at composition,
// which is the only place that does, and a registry is written there and read
// afterwards rather than shared across goroutines.
//
// Every provider it builds goes into the chain bounded by a deadline, behind a
// circuit of its own and under the retry policy, so "no unbounded call to a
// model", "no blind retry" and "a failing endpoint stops being called" are
// properties of the only route to a model rather than habits each provider has
// to remember.
type Registry struct {
	factories map[string]Factory
	public    map[string][]string
	timeout   time.Duration
	retry     vfs.Retry
	breaker   vfs.Breaker
}

// NewRegistry returns a registry holding no kinds, which bounds every provider
// it builds by timeout, retries it by policy and breaks it by breaker. Every
// kind is unknown until something registers it.
//
// The timeout is the gateway's own rather than the one every mount is bounded
// by. A model answers in seconds where a filesystem answers in milliseconds,
// and one deadline covering both is either too short to summarise anything or
// too long to notice a mount that has stopped answering.
func NewRegistry(timeout time.Duration, retry vfs.Retry, breaker vfs.Breaker) *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		public:    make(map[string][]string),
		timeout:   timeout,
		retry:     retry,
		breaker:   breaker,
	}
}

// Register puts factory under kind. It fails with fs.ErrExist on a kind already
// taken, because two factories under one name make which provider a config gets
// depend on registration order.
//
// public names the setting keys this kind is safe to read back to an operator.
// Everything it does not name is a credential as far as anyone else is
// concerned — an API key is the ordinary case here — so a kind that names
// nothing reads back as nothing.
func (r *Registry) Register(kind string, factory Factory, public ...string) error {
	if _, taken := r.factories[kind]; taken {
		return fmt.Errorf("register provider kind %q: %w", kind, fs.ErrExist)
	}

	r.factories[kind] = factory
	r.public[kind] = public
	return nil
}

// Public reports the setting keys kind declared safe to read back. A kind that
// declared none, and a kind nobody registered, have none.
func (r *Registry) Public(kind string) []string {
	return r.public[kind]
}

// Gateway builds every spec and chains it in the order given, which is how a
// config document becomes the one path a model call takes.
//
// It stops at the first spec it cannot honour, so a process never boots on half
// a chain: an endpoint silently missing from the chain is a fallback that is
// not there on the night it is needed. Two endpoints under one name stop it
// too, because a name is how a failure says which one failed.
//
// A chain of nothing is a Gateway that serves nothing, and every accessor on it
// answers nil. That is the laptop with no model configured, and it is a build
// that runs.
func (r *Registry) Gateway(specs []ProviderSpec) (*Gateway, error) {
	gateway := &Gateway{}

	named := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if named[spec.Name] {
			return nil, fmt.Errorf("provider %q: %w", spec.Name, fs.ErrExist)
		}
		named[spec.Name] = true

		provider, err := r.build(spec)
		if err != nil {
			return nil, err
		}
		gateway.links = append(gateway.links, &link{
			name:     spec.Name,
			provider: provider,
			timeout:  r.timeout,
			retry:    r.retry,
			circuit:  vfs.NewCircuit(r.breaker),
		})
	}
	return gateway, nil
}

// build makes what one spec names. A kind no one registered fails with
// fs.ErrNotExist naming what is registered, because a misspelled kind is a boot
// failure worth making legible.
func (r *Registry) build(spec ProviderSpec) (Provider, error) {
	factory, known := r.factories[spec.Kind]
	if !known {
		return nil, fmt.Errorf("provider %q: kind %q is not registered; this build has %s: %w", spec.Name, spec.Kind, r.known(), fs.ErrNotExist)
	}

	provider, err := factory(spec.Settings)
	if err != nil {
		return nil, fmt.Errorf("provider %q of kind %q: %w", spec.Name, spec.Kind, err)
	}
	return provider, nil
}

// known lists the registered kinds for an error message, so a misspelled kind
// at boot shows an operator what the build actually has.
func (r *Registry) known() string {
	kinds := slices.Sorted(maps.Keys(r.factories))
	if len(kinds) == 0 {
		return "no provider kinds"
	}
	return strings.Join(kinds, ", ")
}
