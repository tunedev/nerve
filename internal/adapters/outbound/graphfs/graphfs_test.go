package graphfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// edges is how many claims the graphs in these cases answer with, small enough
// that a case can write out the whole answer.
const edges = 10

// newGraph is a graph with nothing in it, over a store and an extractor a case
// keeps a handle on.
func newGraph(t *testing.T, store *memStore, extract statements) *graphfs.FS {
	t.Helper()

	graph, err := graphfs.Open(t.Context(), "notes", edges, 1, store, store, extract)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	return graph
}

func TestATextWrittenIsATextRead(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})

	if err := graph.Write(t.Context(), "/docs/pumps", []byte("Nerve indexes Postgres")); err != nil {
		t.Fatalf("Write = %v", err)
	}

	body, err := graph.Read(t.Context(), "/docs/pumps")
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	if string(body) != "Nerve indexes Postgres" {
		t.Errorf("Read = %q, want the text that was written", body)
	}
}

// The acceptance of the ticket: a text says two things are related and reading
// the entity's name gives back what it is related to.
func TestExpansionAnswersWithTheNeighboursOfTheEntity(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres\nNerve publishes RabbitMQ\nFuriosa drives Rig")

	answer := read(t, graph, "/neighbours/Nerve")

	want := "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" +
		"Nerve\tmention\tuses\tPostgres\tmention\t1\t1\n" +
		"Nerve\tmention\tpublishes\tRabbitMQ\tmention\t1\t1\n"
	if answer != want {
		t.Errorf("Read(/neighbours/Nerve) =\n%q\nwant\n%q", answer, want)
	}
}

// One hop and no further. Postgres is a neighbour of Nerve and Disk is a
// neighbour of Postgres, so an expansion of Nerve names Postgres and says
// nothing about Disk.
func TestExpansionStopsAtOneHop(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres\nPostgres writes Disk")

	answer := read(t, graph, "/neighbours/Nerve")

	if !strings.Contains(answer, "Postgres") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want the entity one hop away", answer)
	}
	if strings.Contains(answer, "Disk") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want nothing two hops away", answer)
	}
}

// An edge is reached from either end. A graph that answered only for the entity
// a claim was written from would make the direction of a sentence decide what
// is findable.
func TestAnEntityIsFoundAtEitherEndOfAClaim(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	answer := read(t, graph, "/neighbours/Postgres")

	if !strings.Contains(answer, "Nerve\tmention\tuses\tPostgres") {
		t.Errorf("Read(/neighbours/Postgres) = %q, want the claim that names it", answer)
	}
}

// The same claim in two texts is one edge standing on two, which is what makes
// this a graph rather than a log of extractions.
func TestTwoTextsStatingOneClaimMakeOneEdge(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/one", "Nerve uses Postgres")
	write(t, graph, "/docs/two", "Nerve uses Postgres\nNerve uses RabbitMQ")

	answer := read(t, graph, "/neighbours/Nerve")

	want := "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" +
		"Nerve\tmention\tuses\tPostgres\tmention\t2\t1\n" +
		"Nerve\tmention\tuses\tRabbitMQ\tmention\t1\t1\n"
	if answer != want {
		t.Errorf("Read(/neighbours/Nerve) =\n%q\nwant the better-supported claim first,\n%q", answer, want)
	}
}

// An entity nothing relates answers rather than failing: the graph was asked
// and holds nothing about it, which a caller cannot tell from a missing path.
func TestANameWithNothingSaidAboutItStillAnswers(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	if answer := read(t, graph, "/neighbours/Furiosa"); answer != "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" {
		t.Errorf("Read(/neighbours/Furiosa) = %q, want the heading and no claims", answer)
	}
}

// The bound is the whole point of the setting: a well-connected entity has more
// edges than anything reading them has room for.
func TestAnExpansionIsBoundedByTheEdgesSetting(t *testing.T) {
	graph, err := func() (*graphfs.FS, error) {
		store := newMemStore()
		return graphfs.Open(t.Context(), "notes", 2, 1, store, store, statements{})
	}()
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	write(t, graph, "/docs/stack", "Nerve uses Postgres\nNerve uses RabbitMQ\nNerve uses Ollama")

	if rows := strings.Count(read(t, graph, "/neighbours/Nerve"), "\n") - 1; rows != 2 {
		t.Errorf("an expansion carried %d claims, want the 2 the mount was built with", rows)
	}
}

// Removing a text takes what it said with it. A claim two texts made survives
// with less behind it; a claim only the removed text made is gone.
func TestRemovingATextTakesWhatItSaid(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/one", "Nerve uses Postgres")
	write(t, graph, "/docs/two", "Nerve uses Postgres\nNerve uses RabbitMQ")

	if err := graph.Remove(t.Context(), "/docs/two"); err != nil {
		t.Fatalf("Remove = %v", err)
	}

	want := "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" +
		"Nerve\tmention\tuses\tPostgres\tmention\t1\t1\n"
	if answer := read(t, graph, "/neighbours/Nerve"); answer != want {
		t.Errorf("Read(/neighbours/Nerve) =\n%q\nwant\n%q", answer, want)
	}
}

// Rewriting a text replaces what it said. The graph is what the corpus says
// now, not what it has ever said.
func TestRewritingATextReplacesWhatItSaid(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")
	write(t, graph, "/docs/stack", "Nerve uses RabbitMQ")

	answer := read(t, graph, "/neighbours/Nerve")

	if strings.Contains(answer, "Postgres") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want nothing the text no longer says", answer)
	}
	if !strings.Contains(answer, "RabbitMQ") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want what the text says now", answer)
	}
}

// Blob-persisted means exactly this: a second process over the same store opens
// the graph the first one left, without extracting anything again.
func TestAGraphOpensAgainOutOfItsBlob(t *testing.T) {
	store := newMemStore()
	write(t, newGraph(t, store, statements{}), "/docs/stack", "Nerve uses Postgres")

	// The extractor refuses, so anything this graph answers came out of the
	// blob rather than out of a second reading.
	again, err := graphfs.Open(t.Context(), "notes", edges, 1, store, store, statements{refuse: errors.New("no endpoint")})
	if err != nil {
		t.Fatalf("Open = %v", err)
	}

	if answer := read(t, again, "/neighbours/Nerve"); !strings.Contains(answer, "Postgres") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want the claim the first graph was given", answer)
	}
	if body := read(t, again, "/docs/stack"); body != "Nerve uses Postgres" {
		t.Errorf("Read(/docs/stack) = %q, want the text the first graph was given", body)
	}
}

// The blob is the record, so a change it did not take is a change the mount did
// not make. A graph that answered from memory ahead of its store would answer
// something no restart could reproduce.
func TestAChangeTheStoreRefusedIsAChangeTheGraphDidNotMake(t *testing.T) {
	store := newMemStore()
	graph := newGraph(t, store, statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	store.refuse = errors.New("the bucket is not answering")
	if err := graph.Write(t.Context(), "/docs/stack", []byte("Nerve uses RabbitMQ")); err == nil {
		t.Fatal("Write = nil, want the store's refusal")
	}

	if body := read(t, graph, "/docs/stack"); body != "Nerve uses Postgres" {
		t.Errorf("Read(/docs/stack) = %q, want the text the store still holds", body)
	}
	if answer := read(t, graph, "/neighbours/Nerve"); strings.Contains(answer, "RabbitMQ") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want nothing from the write that failed", answer)
	}
}

// An endpoint that will not answer is a write that did not happen, rather than
// a text stored that the graph silently knows nothing about.
func TestATextTheExtractorRefusedIsNotStored(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{refuse: errors.New("no endpoint")})

	if err := graph.Write(t.Context(), "/docs/stack", []byte("Nerve uses Postgres")); err == nil {
		t.Fatal("Write = nil, want the extractor's refusal")
	}
	if _, err := graph.Read(t.Context(), "/docs/stack"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read after a refused write = %v, want fs.ErrNotExist", err)
	}
}

// An empty text has nothing in it to read, so it costs no call at all. The
// conformance suite drives every verb through this path.
func TestAnEmptyTextCostsNoExtraction(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{refuse: errors.New("no endpoint")})

	if err := graph.Create(t.Context(), "/docs/empty"); err != nil {
		t.Fatalf("Create = %v", err)
	}
	if body := read(t, graph, "/docs/empty"); body != "" {
		t.Errorf("Read(/docs/empty) = %q, want nothing", body)
	}
}

// The descriptor varies inside one mount, which is the whole reason capability
// is data rather than a type: the same plugin takes texts at one path and
// refuses to be written at the others.
func TestTheDescriptorVariesByPath(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})

	for path, want := range map[string]vfs.Caps{
		"/docs":             {ObjectStore: true},
		"/docs/stack":       {ObjectStore: true},
		"/neighbours":       {ReadOnly: true},
		"/neighbours/Nerve": {ReadOnly: true},
		"/config":           {ReadOnly: true},
		"/":                 {ReadOnly: true},
	} {
		if got := graph.Capabilities(path); got != want {
			t.Errorf("Capabilities(%q) = %+v, want %+v", path, got, want)
		}
	}
}

// Nothing under the expansions is stored, so there is nothing to enumerate: a
// listing would either be empty or be the set of names somebody happened to
// ask about, and only the first is true.
func TestTheExpansionsListNothing(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	entries, err := graph.ReadDir(t.Context(), "/neighbours")
	if err != nil {
		t.Fatalf("ReadDir(/neighbours) = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/neighbours) listed %d entries, want none", len(entries))
	}
}

// The config path says what the graph is, and the two settings a mount takes
// are both an operator's business.
func TestConfigSaysWhatTheGraphIs(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})

	if got, want := read(t, graph, "/config"), "graph notes\nedges 10\ndepth 1\n"; got != want {
		t.Errorf("Read(/config) = %q, want %q", got, want)
	}
}

// The whole path, with the endpoint the laptop actually has behind it: no
// model, no network, nothing started.
func TestTheLaptopChainAnswersAnExpansion(t *testing.T) {
	store := newMemStore()
	graph, err := graphfs.Open(t.Context(), "notes", edges, 1, store, store, nounlink.New())
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	write(t, graph, "/docs/stack", "Nerve uses Postgres for storage.")

	if answer := read(t, graph, "/neighbours/Nerve"); !strings.Contains(answer, "Postgres") {
		t.Errorf("Read(/neighbours/Nerve) = %q, want the entity nounlink related to it", answer)
	}
}

func write(t *testing.T, graph *graphfs.FS, name, text string) {
	t.Helper()

	if err := graph.Write(t.Context(), name, []byte(text)); err != nil {
		t.Fatalf("Write(%q) = %v", name, err)
	}
}

func read(t *testing.T, graph *graphfs.FS, name string) string {
	t.Helper()

	body, err := graph.Read(t.Context(), name)
	if err != nil {
		t.Fatalf("Read(%q) = %v", name, err)
	}
	return string(body)
}
