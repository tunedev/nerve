package vfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"
)

// Factory builds one plugin from the settings its config names.
//
// Settings are strings because a config file, an environment variable and a
// command line all carry strings. What a setting means is the plugin's
// business, parsed where the plugin is built and never here.
type Factory func(settings map[string]string) (FileSystem, error)

// MountSpec is one line of mount config: where a plugin goes in the tree, which
// kind it is, and what that kind needs to know.
type MountSpec struct {
	// Prefix is the subtree the plugin answers for, rooted and clean.
	Prefix string
	// Kind is the registered name of the factory that builds the plugin.
	Kind string
	// Settings are what that factory needs. A kind that needs nothing takes a
	// nil map.
	Settings map[string]string
}

// Registry maps a kind name onto the factory that builds it, so a new mount is
// a line of config naming a registered kind rather than an edit to a switch or
// a factory function.
//
// Nothing here knows a concrete plugin. Kinds are registered at composition,
// which is the only place that does, and a registry is written there and read
// afterwards rather than shared across goroutines.
//
// Every plugin it builds comes back bounded by timeout, behind the retry policy,
// behind a breaker of its own and under the recorder, so "no unbounded call",
// "no blind retry", "a failing mount stops being called" and "every call is
// measured" are properties of the only route into the tree rather than habits
// each factory has to remember.
//
// It also keeps whichever of them holds something an operating system knows
// about — a connection pool, a broker channel — so that Release can give it
// back. That is what makes lifecycle a matter of two lines at the composition
// root instead of an eleventh verb every kind would have to answer.
type Registry struct {
	factories map[string]Factory
	public    map[string][]string
	timeout   time.Duration
	retry     Retry
	breaker   Breaker
	recorder  Recorder
	held      []io.Closer
}

// NewRegistry returns a registry holding no kinds, which bounds every plugin it
// builds by timeout, retries it by policy and breaks it by breaker. Every kind
// is unknown until something registers it.
//
// All three are parameters rather than constants because all three are
// operational: an operator changes them in config, never in a recompile.
//
// Telemetry starts as NopRecorder rather than as nothing, so the wrapper is on
// every mount from the first build and Record only changes where the reports
// go. A build that exports nothing runs the code path a build that exports
// everything runs.
func NewRegistry(timeout time.Duration, retry Retry, breaker Breaker) *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		public:    make(map[string][]string),
		timeout:   timeout,
		retry:     retry,
		breaker:   breaker,
		recorder:  NopRecorder{},
	}
}

// Register puts factory under kind. It fails with fs.ErrExist on a kind already
// taken, because two factories under one name make which plugin a config gets
// depend on registration order.
//
// public names the setting keys this kind is safe to read back to an operator.
// Everything it does not name is a credential as far as anyone else is
// concerned, so a kind that names nothing reads back as nothing. Only the kind
// knows which of its settings is a secret, and only here does it get to say.
func (r *Registry) Register(kind string, factory Factory, public ...string) error {
	if _, taken := r.factories[kind]; taken {
		return fmt.Errorf("register kind %q: %w", kind, fs.ErrExist)
	}

	r.factories[kind] = factory
	r.public[kind] = public
	return nil
}

// Record sends every call through every mount this registry builds to recorder.
// It is a setter rather than a constructor parameter for the reason Register is
// one: a registry is written at composition and read afterwards, and where the
// reports go is one more thing composition says.
//
// It changes what later builds do and not what earlier ones did. Composition
// registers, records and then builds the table, which is the only order the
// process has.
func (r *Registry) Record(recorder Recorder) { r.recorder = recorder }

// Public reports the setting keys kind declared safe to read back. A kind that
// declared none, and a kind nobody registered, have none.
func (r *Registry) Public(kind string) []string {
	return r.public[kind]
}

// Build makes the plugin one spec names and hands it back bounded, guarded and
// retried, because a mount may be remote and no caller of the tree can tell. A
// kind no one registered fails with fs.ErrNotExist naming what is registered,
// because a misspelled kind is a boot failure worth making legible.
//
// The three wrappers go on in one order and it is the order that makes them
// work together.
//
// The bound goes on first, so every attempt is a call with its own deadline.
// The other order gives the attempts one deadline to share, and an attempt that
// hangs then spends the whole of it — leaving the retries to expire on arrival,
// which is a retry policy that does nothing when it is most needed. The
// sequence is bounded even so: each attempt runs on the caller's context, and
// the waits between them end when it does.
//
// The breaker goes on next, between the bound and the retry, so it counts
// attempts rather than sequences and the retry above it stops the moment it
// opens. Outside the retry it would see one failure per sequence, trip after
// attempts times threshold calls, and let a mount that is plainly down be
// hammered the whole way there.
//
// Every mount gets its own breaker, built here, because a breaker shared
// between mounts would take a working one down with a broken one.
//
// The recorder goes on last and therefore outermost, so what it times is the
// call the caller made rather than one attempt of it. It is also the only layer
// that knows which mount it is on: the spec is here and nowhere below.
func (r *Registry) Build(spec MountSpec) (FileSystem, error) {
	fsys, err := r.Plugin(spec)
	if err != nil {
		return nil, err
	}
	guarded := WithRetry(WithBreaker(WithTimeout(fsys, r.timeout), r.breaker), r.retry)
	return WithTelemetry(guarded, r.recorder, spec.Prefix, spec.Kind), nil
}

// Plugin builds what one spec names, bare: no bound, no breaker, no retry. It
// is what a kind that composes another kind builds its target with, and the
// only reason it is separate from Build.
//
// Wrapped twice, one call is retried by two layers and a dependency already
// failing is hit attempts times attempts. The outer mount is wrapped by Build
// on the way into the table, so the composed target must not be wrapped again
// here: exactly one layer retries a given call.
//
// This is also where a plugin that holds a resource is noticed, by the one
// assertion in this package that is sound: what it interrogates is the plugin
// the factory just returned, before anything wraps it. Everywhere else the
// value in hand is the dispatcher, which is why capability discovery goes
// through descriptors and accessors instead. A composed target is noticed here
// too, and it is built before the mount over it, which is what Release reads
// the order backwards for.
func (r *Registry) Plugin(spec MountSpec) (FileSystem, error) {
	factory, known := r.factories[spec.Kind]
	if !known {
		return nil, fmt.Errorf("mount %q: kind %q is not registered; this build has %s: %w", spec.Prefix, spec.Kind, r.known(), fs.ErrNotExist)
	}

	fsys, err := factory(spec.Settings)
	if err != nil {
		return nil, fmt.Errorf("mount %q of kind %q: %w", spec.Prefix, spec.Kind, err)
	}
	if closer, holds := fsys.(io.Closer); holds {
		r.held = append(r.held, closer)
	}
	return fsys, nil
}

// Release gives back everything the plugins this registry built are holding,
// and reports every refusal rather than the first: a database that will not
// close is not a reason to leave a broker connection open.
//
// It releases in the reverse of the order it built, so a kind that composes
// another lets go before its target does. What it has released it no longer
// holds, which makes a second call the no-op a drain path wants it to be.
//
// The composition root calls this once the server has stopped answering. Until
// then something may still be mid-verb on a mount, and releasing under a call
// in flight turns a shutdown into an error the caller cannot do anything about.
func (r *Registry) Release() error {
	held := r.held
	r.held = nil

	var refused []error
	for i := len(held) - 1; i >= 0; i-- {
		if err := held[i].Close(); err != nil {
			refused = append(refused, fmt.Errorf("release: %w", err))
		}
	}
	return errors.Join(refused...)
}

// Table builds every spec and mounts it, which is how a config document becomes
// the tree a caller holds. It stops at the first spec it cannot honour, so a
// process never boots on half a mount table.
func (r *Registry) Table(specs []MountSpec) (*MountTable, error) {
	table := NewMountTable()

	for _, spec := range specs {
		fsys, err := r.Build(spec)
		if err != nil {
			return nil, err
		}
		if err := table.Mount(spec.Prefix, fsys); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// known lists the registered kinds for an error message, so a misspelled kind
// at boot shows an operator what the build actually has.
func (r *Registry) known() string {
	kinds := slices.Sorted(maps.Keys(r.factories))
	if len(kinds) == 0 {
		return "no kinds"
	}
	return strings.Join(kinds, ", ")
}
