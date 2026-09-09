package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/llm"
	"github.com/tunedev/nerve/internal/core/stability"
)

// The policies every test here builds a chain with unless it says otherwise:
// one attempt so a failure is one call, a circuit that opens on the first
// transient failure, and a deadline no test reaches.
var (
	once     = stability.Retry{Attempts: 1}
	prompt   = stability.Breaker{Threshold: 1, Cooldown: time.Minute}
	patience = time.Minute
)

// errTransient is a far end wobbling: the one class another attempt, or another
// endpoint, might not see.
var errTransient = fmt.Errorf("connection reset: %w", stability.ErrTransient)

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

// vectors answers Embed with one vector per text, counting the calls that
// reached it. It embeds and does not summarise, which is the mixed chain a
// build with a summariser and an embedding model actually has.
type vectors struct {
	llm.NoAccessors
	err   error
	calls int
}

func (v *vectors) AsEmbedder() llm.Embedder { return v }

func (v *vectors) Embed(_ context.Context, texts []string) ([][]float32, error) {
	v.calls++
	if v.err != nil {
		return nil, v.err
	}
	answer := make([][]float32, len(texts))
	for i, text := range texts {
		answer[i] = []float32{float32(len(text))}
	}
	return answer, nil
}

// named answers ExtractEntities with one entity per capitalised word and the
// relation between the first two, counting the calls that reached it. It reads
// entities and neither summarises nor embeds, which is what a chain holding a
// summariser and an entity reader looks like.
type named struct {
	llm.NoAccessors
	err   error
	calls int
}

func (n *named) AsEntityExtractor() llm.EntityExtractor { return n }

func (n *named) ExtractEntities(_ context.Context, text string) (llm.Extraction, error) {
	n.calls++
	if n.err != nil {
		return llm.Extraction{}, n.err
	}

	var found llm.Extraction
	for _, word := range strings.Fields(text) {
		found.Entities = append(found.Entities, llm.Entity{Kind: "thing", Name: word})
	}
	if len(found.Entities) >= 2 {
		found.Relations = []llm.Relation{{From: found.Entities[0], Predicate: "beside", To: found.Entities[1]}}
	}
	return found, nil
}

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
	if verb := gateway.AsFactExtractor(); verb != nil {
		t.Errorf("AsFactExtractor = %v, want nil", verb)
	}
	if verb := gateway.AsEntityExtractor(); verb != nil {
		t.Errorf("AsEntityExtractor = %v, want nil", verb)
	}
	if served := gateway.Capabilities(); len(served) != 0 {
		t.Errorf("Capabilities = %v, want none", served)
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

// A capability is served by the providers whose accessor answers it, and by no
// others. A chain holding a summariser and an embedding model routes each verb
// to the endpoint that has it without a table saying which is which — the
// summariser is never asked for a vector.
func TestEachCapabilityReachesTheProviderThatServesIt(t *testing.T) {
	said := &endpoint{answer: "the pumps had not run"}
	embedded := &vectors{}
	gateway := chain(t, said, embedded)

	answer, err := gateway.AsEmbedder().Embed(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(answer) != 2 {
		t.Fatalf("Embed returned %d vectors, want 2", len(answer))
	}
	if said.calls != 0 {
		t.Errorf("the summarising provider saw %d calls, want 0: it serves no embedding", said.calls)
	}

	if _, err := gateway.AsSummariser().Summarise(context.Background(), "the pumps had not run", 1); err != nil {
		t.Fatalf("Summarise = %v", err)
	}
	if embedded.calls != 1 {
		t.Errorf("the embedding provider saw %d calls, want the one embedding: it summarises nothing", embedded.calls)
	}
}

// A chain of two endpoints running one model is what an embedding failover is
// for, so a wobbling one passes to the next exactly as a summariser does.
func TestATransientEmbeddingFailurePassesToTheNextProvider(t *testing.T) {
	first := &vectors{err: errTransient}
	second := &vectors{}

	answer, err := chain(t, first, second).AsEmbedder().Embed(context.Background(), []string{"one"})
	if err != nil {
		t.Fatalf("Embed = %v", err)
	}
	if len(answer) != 1 {
		t.Fatalf("Embed returned %d vectors, want 1", len(answer))
	}
	if second.calls != 1 {
		t.Errorf("the second provider saw %d calls, want 1", second.calls)
	}
}

// The banner and the operator surface read back every capability the chain
// serves, so a build wired to endpoints that serve nothing it needs looks wired
// and is not.
func TestTheChainNamesEveryCapabilityItServes(t *testing.T) {
	served := chain(t, &endpoint{}, &vectors{}).Capabilities()
	if len(served) != 2 || served[0] != "summarise" || served[1] != "embed" {
		t.Errorf("Capabilities = %v, want summarise then embed", served)
	}
}

// A chain that summarises and embeds nothing says so, rather than handing back
// a routed verb over no endpoint.
func TestAChainWithNoEmbeddingEndpointAnswersNil(t *testing.T) {
	gateway := chain(t, &endpoint{})

	if verb := gateway.AsEmbedder(); verb != nil {
		t.Errorf("AsEmbedder = %v, want nil: no provider in the chain embeds", verb)
	}
	if served := gateway.Capabilities(); len(served) != 1 || served[0] != "summarise" {
		t.Errorf("Capabilities = %v, want summarise alone", served)
	}
}

// extractor answers Extract with whatever a test set. It summarises nothing,
// which is what makes it the control for the capability routing below.
type extractor struct {
	llm.NoAccessors
	facts []llm.Fact
	err   error
	calls int
}

func (e *extractor) AsFactExtractor() llm.FactExtractor { return e }

func (e *extractor) Extract(context.Context, []llm.Turn) ([]llm.Fact, error) {
	e.calls++
	return e.facts, e.err
}

// Each capability is routed over the providers that serve that capability, and
// not over the chain. An endpoint that summarises and does not extract is
// passed over by extraction without being called, which is what lets one chain
// hold a summarising endpoint and an extracting one.
func TestACapabilityIsRoutedOverTheProvidersThatServeIt(t *testing.T) {
	summariser := &endpoint{answer: "a summary"}
	facts := &extractor{facts: []llm.Fact{{Kind: "preference", Body: "tabs", Priority: llm.High}}}

	extracted, err := chain(t, summariser, facts).AsFactExtractor().Extract(t.Context(), []llm.Turn{{Role: "user", Body: "I prefer tabs"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(extracted) != 1 || extracted[0].Body != "tabs" || extracted[0].Priority != llm.High {
		t.Errorf("Extract = %+v, want the extracting endpoint's one fact", extracted)
	}
	if summariser.calls != 0 {
		t.Errorf("the summarising endpoint saw %d calls, want 0: it does not extract", summariser.calls)
	}
}

// A transient failure passes extraction to the next extracting endpoint, the
// same way it passes a summary. The stability policy is the link's rather than
// the capability's, so a second capability does not need a second one.
func TestExtractionPassesADegradedEndpointToTheNext(t *testing.T) {
	failing := &extractor{err: errTransient}
	answering := &extractor{facts: []llm.Fact{{Kind: "instruction", Body: "never force push", Priority: llm.High}}}

	extracted, err := chain(t, failing, answering).AsFactExtractor().Extract(t.Context(), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(extracted) != 1 || extracted[0].Kind != "instruction" {
		t.Errorf("Extract = %+v, want the second endpoint's fact", extracted)
	}
	if failing.calls != 1 || answering.calls != 1 {
		t.Errorf("calls = %d then %d, want one each", failing.calls, answering.calls)
	}
}

// A chain that extracts and does not summarise says so. What a build serves is
// what its endpoints serve between them, and an operator reading the banner is
// asking which stages of the pipeline this process can actually run.
func TestTheChainNamesTheCapabilitiesItsProvidersServe(t *testing.T) {
	served := chain(t, &extractor{}).Capabilities()
	if len(served) != 1 || served[0] != "extract" {
		t.Errorf("Capabilities = %v, want extract alone", served)
	}
	if verb := chain(t, &extractor{}).AsSummariser(); verb != nil {
		t.Errorf("AsSummariser = %v, want nil: the chain only extracts", verb)
	}
}

// writer answers WriteSQL with whatever a test set. It serves nothing else,
// which is what makes it the control for the routing below.
type writer struct {
	llm.NoAccessors
	sql   string
	err   error
	calls int
	table llm.Table
}

func (w *writer) AsSQLWriter() llm.SQLWriter { return w }

func (w *writer) WriteSQL(_ context.Context, _ string, table llm.Table) (string, error) {
	w.calls++
	w.table = table
	return w.sql, w.err
}

// SQL writing is routed over the endpoints that write SQL and over no others,
// and the table the caller described reaches the endpoint that answered.
func TestTheSQLWritingEndpointIsTheOneAskedForAStatement(t *testing.T) {
	summariser := &endpoint{answer: "a summary"}
	sql := &writer{sql: "SELECT stage FROM applications"}
	table := llm.Table{Name: "applications", Columns: []llm.Column{{Name: "stage", Type: "string"}}}

	statement, err := chain(t, summariser, sql).AsSQLWriter().WriteSQL(t.Context(), "which stages are there", table)
	if err != nil {
		t.Fatalf("WriteSQL: %v", err)
	}
	if statement != "SELECT stage FROM applications" {
		t.Errorf("WriteSQL = %q, want the writing endpoint's statement", statement)
	}
	if sql.table.Name != "applications" || len(sql.table.Columns) != 1 {
		t.Errorf("the endpoint was given table %+v, want the one the caller described", sql.table)
	}
	if summariser.calls != 0 {
		t.Errorf("the summarising endpoint saw %d calls, want 0: it writes no SQL", summariser.calls)
	}
}

// A degraded endpoint passes the question to the next one that writes SQL, the
// same way it passes a summary.
func TestSQLWritingPassesADegradedEndpointToTheNext(t *testing.T) {
	failing := &writer{err: errTransient}
	answering := &writer{sql: "SELECT count(*) FROM applications"}

	statement, err := chain(t, failing, answering).AsSQLWriter().WriteSQL(t.Context(), "how many", llm.Table{Name: "applications"})
	if err != nil {
		t.Fatalf("WriteSQL: %v", err)
	}
	if statement != "SELECT count(*) FROM applications" {
		t.Errorf("WriteSQL = %q, want the second endpoint's statement", statement)
	}
	if failing.calls != 1 || answering.calls != 1 {
		t.Errorf("calls = %d then %d, want one each", failing.calls, answering.calls)
	}
}

// A chain with no endpoint that writes SQL says so where a build wires the
// query path, rather than on the first question somebody asks.
func TestAChainWithNoSQLWritingEndpointAnswersNil(t *testing.T) {
	gateway := chain(t, &endpoint{})

	if verb := gateway.AsSQLWriter(); verb != nil {
		t.Errorf("AsSQLWriter = %v, want nil: no provider in the chain writes SQL", verb)
	}
	if served := chain(t, &writer{}).Capabilities(); len(served) != 1 || served[0] != "write-sql" {
		t.Errorf("Capabilities = %v, want write-sql alone", served)
	}
}

// Entity extraction is routed like every other capability: it reaches the
// endpoints whose accessor answered and no others, so a chain holding a
// summariser and an entity reader asks each for what it has.
func TestEntityExtractionReachesTheProviderThatReadsIt(t *testing.T) {
	said := &endpoint{answer: "the pumps had not run"}
	reader := &named{}
	gateway := chain(t, said, reader)

	if served := gateway.Capabilities(); !slices.Contains(served, "entities") {
		t.Fatalf("the gateway serves %v, want entity extraction", served)
	}

	found, err := gateway.AsEntityExtractor().ExtractEntities(context.Background(), "Nerve Postgres")
	if err != nil {
		t.Fatalf("ExtractEntities = %v", err)
	}
	if len(found.Entities) != 2 || len(found.Relations) != 1 {
		t.Fatalf("ExtractEntities = %+v, want two entities and one relation", found)
	}
	if said.calls != 0 {
		t.Errorf("the summarising provider saw %d calls, want 0: it reads no entities", said.calls)
	}
}

// A chain of two endpoints reading entities fails over exactly as every other
// capability does, because the failover rule is written once and the accessor
// is what varies.
func TestATransientEntityFailurePassesToTheNextProvider(t *testing.T) {
	first := &named{err: errTransient}
	second := &named{}

	found, err := chain(t, first, second).AsEntityExtractor().ExtractEntities(context.Background(), "Nerve")
	if err != nil {
		t.Fatalf("ExtractEntities = %v", err)
	}
	if len(found.Entities) != 1 {
		t.Fatalf("ExtractEntities = %+v, want the second endpoint's answer", found)
	}
	if second.calls != 1 {
		t.Errorf("the second endpoint saw %d calls, want 1", second.calls)
	}
}

// folding answers Consolidate by merging the first two facts it is shown,
// counting the calls that reached it. It merges facts and does nothing else,
// which is the control for the routing below.
type folding struct {
	llm.NoAccessors
	err   error
	calls int
	saw   []llm.Fact
}

func (f *folding) AsFactConsolidator() llm.FactConsolidator { return f }

func (f *folding) Consolidate(_ context.Context, facts []llm.Fact) ([]llm.Narrative, error) {
	f.calls++
	f.saw = facts
	if f.err != nil {
		return nil, f.err
	}
	return []llm.Narrative{{Facts: []int{0, 1}, Body: facts[0].Body + " and " + facts[1].Body}}, nil
}

// Consolidation is routed over the endpoints that merge facts and over no
// others, and the facts the caller was shown reach the endpoint that answered.
func TestTheConsolidatingEndpointIsTheOneAskedToMerge(t *testing.T) {
	said := &endpoint{answer: "a summary"}
	merge := &folding{}
	facts := []llm.Fact{
		{Kind: "preference", Body: "prefers tabs", Priority: llm.Normal},
		{Kind: "preference", Body: "prefers tabs in Go", Priority: llm.Normal},
	}

	narratives, err := chain(t, said, merge).AsFactConsolidator().Consolidate(t.Context(), facts)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(narratives) != 1 || narratives[0].Body != "prefers tabs and prefers tabs in Go" {
		t.Errorf("Consolidate = %+v, want the merging endpoint's narrative", narratives)
	}
	if !slices.Equal(merge.saw, facts) {
		t.Errorf("the endpoint was shown %+v, want the facts the caller had", merge.saw)
	}
	if said.calls != 0 {
		t.Errorf("the summarising endpoint saw %d calls, want 0: it merges nothing", said.calls)
	}
}

// A degraded endpoint passes the facts to the next one that merges them, the
// same way it passes a summary: the failover rule is written once and the
// accessor is what varies.
func TestConsolidationPassesADegradedEndpointToTheNext(t *testing.T) {
	failing, answering := &folding{err: errTransient}, &folding{}
	facts := []llm.Fact{
		{Kind: "instruction", Body: "never force push", Priority: llm.High},
		{Kind: "instruction", Body: "never force push on main", Priority: llm.High},
	}

	narratives, err := chain(t, failing, answering).AsFactConsolidator().Consolidate(t.Context(), facts)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(narratives) != 1 {
		t.Fatalf("Consolidate = %+v, want the second endpoint's narrative", narratives)
	}
	if failing.calls != 1 || answering.calls != 1 {
		t.Errorf("calls = %d then %d, want one each", failing.calls, answering.calls)
	}
}

// A chain with no endpoint that merges facts says so where a build wires the
// commit pipeline, rather than on the first conversation it commits.
func TestAChainWithNoConsolidatingEndpointAnswersNil(t *testing.T) {
	if verb := chain(t, &endpoint{}).AsFactConsolidator(); verb != nil {
		t.Errorf("AsFactConsolidator = %v, want nil: no provider in the chain merges facts", verb)
	}
	if served := chain(t, &folding{}).Capabilities(); len(served) != 1 || served[0] != "consolidate" {
		t.Errorf("Capabilities = %v, want consolidate alone", served)
	}
}
