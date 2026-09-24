package graphfs_test

import (
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
)

// deepGraph is a graph with nothing in it that answers an expansion from
// depth hops out, with at most bound claims.
func deepGraph(t *testing.T, bound, depth int) *graphfs.FS {
	t.Helper()

	store := newMemStore()
	graph, err := graphfs.Open(t.Context(), "notes", bound, depth, store, store, statements{})
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	return graph
}

// The acceptance of the ticket: Disk is two hops out of Nerve and comes back,
// Blocks is three hops out and does not, and Rig is joined to nothing Nerve
// reaches.
func TestATwoHopExpansionReachesTwoHopsAndNoFurther(t *testing.T) {
	graph := deepGraph(t, edges, 2)
	write(t, graph, "/docs/stack", "Nerve uses Postgres\nPostgres writes Disk\nDisk holds Blocks\nFuriosa drives Rig")

	answer := read(t, graph, "/neighbours/Nerve")

	want := "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" +
		"Nerve\tmention\tuses\tPostgres\tmention\t1\t1\n" +
		"Postgres\tmention\twrites\tDisk\tmention\t1\t2\n"
	if answer != want {
		t.Errorf("Read(/neighbours/Nerve) =\n%q\nwant\n%q", answer, want)
	}
}

// The bound is on the answer, not on each hop. Nerve's two best-supported
// neighbours are Postgres and RabbitMQ; Ollama is its weakest, and the claim
// the corpus repeats most is one hop beyond Ollama. A walk that cut every hop
// to the bound would never follow Ollama and never find Model.
func TestTheBoundAppliesToTheAnswerNotToEachHop(t *testing.T) {
	graph := deepGraph(t, 2, 2)
	write(t, graph, "/docs/a", "Nerve uses Postgres\nNerve uses RabbitMQ")
	write(t, graph, "/docs/b", "Nerve uses Postgres\nNerve uses RabbitMQ")
	write(t, graph, "/docs/c", "Nerve uses Ollama\nOllama runs Model")
	write(t, graph, "/docs/d", "Ollama runs Model")
	write(t, graph, "/docs/e", "Ollama runs Model")

	answer := read(t, graph, "/neighbours/Nerve")

	want := "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" +
		"Ollama\tmention\truns\tModel\tmention\t3\t2\n" +
		"Nerve\tmention\tuses\tPostgres\tmention\t2\t1\n"
	if answer != want {
		t.Errorf("Read(/neighbours/Nerve) =\n%q\nwant\n%q", answer, want)
	}
}

// A claim is reported once, at the hop it was first reached. The edge back from
// Postgres to Nerve is the same edge as the one out, and two neighbours of
// Nerve that are related to each other are related by one claim.
func TestAClaimReachedTwiceIsReportedOnceAtItsNearestHop(t *testing.T) {
	graph := deepGraph(t, edges, 3)
	write(t, graph, "/docs/stack", "Nerve uses Postgres\nNerve uses RabbitMQ\nRabbitMQ feeds Postgres")

	answer := read(t, graph, "/neighbours/Nerve")

	want := "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n" +
		"Nerve\tmention\tuses\tPostgres\tmention\t1\t1\n" +
		"Nerve\tmention\tuses\tRabbitMQ\tmention\t1\t1\n" +
		"RabbitMQ\tmention\tfeeds\tPostgres\tmention\t1\t2\n"
	if answer != want {
		t.Errorf("Read(/neighbours/Nerve) =\n%q\nwant\n%q", answer, want)
	}
}

// Depth is what the mount says it is, and the config path says so.
func TestTheDepthIsReadBackFromTheConfig(t *testing.T) {
	graph := deepGraph(t, edges, 3)

	if answer := read(t, graph, "/config"); !strings.Contains(answer, "depth 3\n") {
		t.Errorf("Read(/config) = %q, want the depth the mount was opened with", answer)
	}
}
