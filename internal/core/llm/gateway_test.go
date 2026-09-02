package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The policies every test here builds a chain with unless it says otherwise:
// one attempt so a failure is one call, a circuit that opens on the first
// transient failure, and a deadline no test reaches.
var (
	once     = vfs.Retry{Attempts: 1}
	prompt   = vfs.Breaker{Threshold: 1, Cooldown: time.Minute}
	patience = time.Minute
)

// errTransient is a far end wobbling: the one class another attempt, or another
// endpoint, might not see.
var errTransient = fmt.Errorf("connection reset: %w", vfs.ErrTransient)

// endpoint answers Summarise with whatever a test set, counting the calls that
// reached it. It is how a test moves one provider in a chain between failing
// and answering.
type endpoint struct {
	llm.NoAccessors
	answer string
	err    error
	hangs  bool
	calls  int
}

func (e *endpoint) AsSummariser() llm.Summariser { return e }

func (e *endpoint) Summarise(ctx context.Context, _ string, _ int) (string, error) {
	e.calls++
	if e.hangs {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return e.answer, e.err
}

// mute is a provider that serves nothing, which is every provider before it
// overrides an accessor.
type mute struct{ llm.NoAccessors }

// chain builds a gateway over the providers given, in order, under the policies
// above. Every kind is registered here rather than in a config document,
// because what a test varies is the chain and never the factory.
func chain(t *testing.T, providers ...llm.Provider) *llm.Gateway {
	t.Helper()
	return chainWithin(t, patience, providers...)
}

// chainWithin is chain with the per-attempt deadline named, for the one test
// that is about the deadline.
func chainWithin(t *testing.T, timeout time.Duration, providers ...llm.Provider) *llm.Gateway {
	t.Helper()

	registry := llm.NewRegistry(timeout, once, prompt)
	specs := make([]llm.ProviderSpec, len(providers))
	for i, provider := range providers {
		kind := fmt.Sprintf("kind%d", i)
		if err := registry.Register(kind, func(map[string]string) (llm.Provider, error) { return provider, nil }); err != nil {
			t.Fatalf("Register(%q) = %v", kind, err)
		}
		specs[i] = llm.ProviderSpec{Name: fmt.Sprintf("provider%d", i), Kind: kind}
	}

	gateway, err := registry.Gateway(specs)
	if err != nil {
		t.Fatalf("Gateway(%v) = %v", specs, err)
	}
	return gateway
}

// summarise is one routed call, for a test that cares about the answer rather
// than about how it asked.
func summarise(t *testing.T, gateway *llm.Gateway) (string, error) {
	t.Helper()

	summariser := gateway.AsSummariser()
	if summariser == nil {
		t.Fatal("AsSummariser = nil, want the routed verb")
	}
	return summariser.Summarise(context.Background(), "a document", 1)
}

// The chain is asked in order and stops at the first answer. A caller holding
// the gateway cannot tell how many endpoints stand behind it, which is what
// makes putting a hosted model in front of the laptop's a line of config.
func TestTheFirstProviderThatAnswersIsTheAnswer(t *testing.T) {
	first, second := &endpoint{answer: "from the first"}, &endpoint{answer: "from the second"}

	answer, err := summarise(t, chain(t, first, second))
	if err != nil || answer != "from the first" {
		t.Errorf("Summarise = %q, %v, want the first provider's answer", answer, err)
	}
	if second.calls != 0 {
		t.Errorf("the second provider saw %d calls, want 0: a chain that answered kept asking", second.calls)
	}
}

// One flaky endpoint degrades a feature rather than taking down the path that
// used it. A transient failure the attempts could not outlast is the endpoint
// failing, not the call being wrong, so the next endpoint is asked.
func TestATransientFailurePassesToTheNextProvider(t *testing.T) {
	failing, working := &endpoint{err: errTransient}, &endpoint{answer: "from the second"}

	answer, err := summarise(t, chain(t, failing, working))
	if err != nil || answer != "from the second" {
		t.Errorf("Summarise = %q, %v, want the second provider's answer", answer, err)
	}
	if failing.calls != once.Attempts {
		t.Errorf("the failing provider saw %d calls, want %d", failing.calls, once.Attempts)
	}
}

// A fail-fast failure stops the chain where it is. The next model refuses the
// same invalid request, so asking it is a slower failure that costs a second
// dependency on the way.
func TestAFailFastFailureStopsTheChain(t *testing.T) {
	refusing, working := &endpoint{err: fs.ErrInvalid}, &endpoint{answer: "never reached"}

	_, err := summarise(t, chain(t, refusing, working))
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Summarise = %v, want the refusal", err)
	}
	if working.calls != 0 {
		t.Errorf("the second provider saw %d calls, want 0: a refusal is an answer", working.calls)
	}
}

// The failure a caller reads names the endpoint that gave it. A chain that
// reported only the error would leave an operator with two endpoints and no
// way to tell which one is down.
func TestAFailureNamesTheProviderThatGaveIt(t *testing.T) {
	_, err := summarise(t, chain(t, &endpoint{err: fs.ErrInvalid}))
	if err == nil || !strings.Contains(err.Error(), `provider "provider0"`) {
		t.Errorf("Summarise = %v, want the provider named", err)
	}
}

// Each endpoint has its own circuit. Once one has failed enough to be given up
// on, the chain passes over it without calling it and the endpoint beside it
// never notices.
func TestAnOpenCircuitIsPassedOverWithoutACall(t *testing.T) {
	broken, working := &endpoint{err: errTransient}, &endpoint{answer: "from the second"}
	gateway := chain(t, broken, working)

	if _, err := summarise(t, gateway); err != nil {
		t.Fatalf("the first Summarise = %v, want the second provider's answer", err)
	}

	answer, err := summarise(t, gateway)
	if err != nil || answer != "from the second" {
		t.Errorf("Summarise = %q, %v, want the second provider's answer", answer, err)
	}
	if broken.calls != prompt.Threshold {
		t.Errorf("the broken provider saw %d calls, want %d: the open circuit is still calling it", broken.calls, prompt.Threshold)
	}
	if working.calls != 2 {
		t.Errorf("the working provider saw %d calls, want 2: a broken endpoint beside it changed what it answers", working.calls)
	}
}

// Every attempt is bounded. A model that has stopped answering must not hang
// the caller until the caller's own deadline, and an endpoint out of time is
// an endpoint failing, so the chain moves on.
func TestAProviderOutOfTimeIsPassedOver(t *testing.T) {
	hanging, working := &endpoint{hangs: true}, &endpoint{answer: "from the second"}

	answer, err := summarise(t, chainWithin(t, 10*time.Millisecond, hanging, working))
	if err != nil || answer != "from the second" {
		t.Errorf("Summarise = %q, %v, want the second provider's answer", answer, err)
	}
}

// A caller that has gone gets no further endpoint. Spending a second
// dependency on an answer nobody is waiting for is the cost, and the failure
// worth reading is the last real one rather than the cancellation.
func TestACancelledCallerStopsTheChain(t *testing.T) {
	first, second := &endpoint{err: errTransient}, &endpoint{answer: "never reached"}
	gateway := chain(t, first, second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := gateway.AsSummariser().Summarise(ctx, "a document", 1); err == nil {
		t.Error("Summarise = nil, want a failure")
	}
	if second.calls != 0 {
		t.Errorf("the second provider saw %d calls, want 0: the caller had gone", second.calls)
	}
}

// A capability no endpoint serves is answered with nil, exactly as a provider
// answers it. A build wiring a pipeline finds that out where it wires it,
// rather than on the first document.
func TestAnUnservedCapabilityAnswersNil(t *testing.T) {
	if verb := chain(t, &mute{}).AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil: no provider in the chain summarises", verb)
	}
}

// A build with no providers configured runs. It serves nothing, which is the
// laptop with no model, and every accessor says so.
func TestAnEmptyChainServesNothing(t *testing.T) {
	gateway := chain(t)

	if verb := gateway.AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil", verb)
	}
	if names := gateway.Providers(); len(names) != 0 {
		t.Errorf("Providers = %v, want none", names)
	}
}

// The chain reads back in the order it is asked, which is what a banner and an
// operator surface print.
func TestTheChainNamesItselfInOrder(t *testing.T) {
	names := chain(t, &endpoint{}, &mute{}).Providers()
	if len(names) != 2 || names[0] != "provider0" || names[1] != "provider1" {
		t.Errorf("Providers = %v, want provider0 then provider1", names)
	}
}
