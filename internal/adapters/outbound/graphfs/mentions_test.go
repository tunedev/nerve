package graphfs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/nounlink"
)

// The texts that say most about an entity come first, and a text that only
// names it still answers: most of a corpus names things and relates none of
// them, and a ranking that dropped those would answer nothing for most of it.
func TestMentionsRankTheTextsThatSayMostFirst(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/quiet", "Nerve is\nsomething else entirely")
	write(t, graph, "/docs/loud", "Nerve uses Postgres\nNerve publishes RabbitMQ\nFuriosa drives Rig")
	write(t, graph, "/docs/one", "Nerve uses Postgres")
	write(t, graph, "/docs/other", "Furiosa drives Rig")

	answer := read(t, graph, "/mentions/Nerve")

	want := "key\tclaims\n" +
		"loud\t2\n" +
		"one\t1\n"
	if answer != want {
		t.Errorf("Read(/mentions/Nerve) =\n%q\nwant\n%q", answer, want)
	}
}

// A text that names an entity and relates it to nothing is still a text about
// it, and it sorts behind every text that stated a claim. This is the ordinary
// case rather than the corner: most of a corpus names things and relates none
// of them, which is what the real extractor is here to show.
func TestMentionsIncludeATextThatOnlyNamesTheEntity(t *testing.T) {
	store := newMemStore()
	graph, err := graphfs.Open(t.Context(), "notes", edges, 1, store, store, nounlink.New())
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	write(t, graph, "/docs/claim", "Nerve uses Postgres for storage.")
	write(t, graph, "/docs/aside", "Postgres was mentioned again.")

	answer := read(t, graph, "/mentions/Postgres")

	want := "key\tclaims\n" +
		"claim\t1\n" +
		"aside\t0\n"
	if answer != want {
		t.Errorf("Read(/mentions/Postgres) =\n%q\nwant\n%q", answer, want)
	}
}

// A name no text names is an answer and not a missing path, for the reason an
// expansion of an unknown entity is: the graph was asked and holds nothing.
func TestMentionsOfAnUnknownEntityAnswerWithNoRows(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	if answer := read(t, graph, "/mentions/Immortan"); answer != "key\tclaims\n" {
		t.Errorf("Read(/mentions/Immortan) = %q, want the heading and no rows", answer)
	}
}

// The mentions are bounded by the same number an expansion is: one setting says
// how much of an answer a graph hands back, whichever answer it is.
func TestMentionsAreBoundedByTheEdgesSetting(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	for at := range edges + 5 {
		write(t, graph, "/docs/"+string(rune('a'+at)), "Nerve uses Postgres")
	}

	rows := strings.Count(read(t, graph, "/mentions/Nerve"), "\n") - 1
	if rows != edges {
		t.Errorf("the mentions answered with %d texts, want the %d the setting bounds it to", rows, edges)
	}
}

// The mentions hold nothing, so there is nothing to list: every name under them
// is a question rather than an entry.
func TestTheMentionsListNothing(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})
	write(t, graph, "/docs/stack", "Nerve uses Postgres")

	entries, err := graph.ReadDir(t.Context(), "/mentions")
	if err != nil {
		t.Fatalf("ReadDir(/mentions) = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(/mentions) listed %d entries, want none", len(entries))
	}
}

// The mentions are the graph's shape, not an entry a caller rearranges.
func TestTheMentionsRefuseAWrite(t *testing.T) {
	graph := newGraph(t, newMemStore(), statements{})

	err := graph.Write(t.Context(), "/mentions/Nerve", []byte("anything"))
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Write(/mentions/Nerve) = %v, want fs.ErrPermission", err)
	}
}
