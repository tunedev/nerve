package relation_test

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/relation"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/relations"

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}

	tailor = relation.Ref{Kind: "skill", Name: "tailor-cv"}
	cv     = relation.Ref{Kind: "blueprint", Name: "cv"}
	letter = relation.Ref{Kind: "blueprint", Name: "cover-letter"}
)

// plugins are the two shapes a curated graph can sit on: a tree, where the
// subject of an edge is a directory something has to make, and a keyspace,
// where it is a prefix that exists because keys are under it. Every verb is
// asked on both, because which one a mount is holds no interest above the port.
var plugins = map[string]func() vfs.FileSystem{
	"tree":     func() vfs.FileSystem { return memfs.New() },
	"keyspace": func() vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "curated") },
}

func newGraph(t *testing.T, plugin func() vfs.FileSystem) (*relation.Graph, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	graph, err := relation.NewGraph(table, root)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return graph, table
}

// onBoth runs one case against every plugin, so a promise kept over a tree and
// broken over a keyspace fails rather than passing on the half that was tried.
func onBoth(t *testing.T, run func(t *testing.T, graph *relation.Graph)) {
	t.Helper()
	onBothWithFS(t, func(t *testing.T, graph *relation.Graph, _ vfs.FileSystem) { run(t, graph) })
}

// onBothWithFS is onBoth for a case that has to put something on the mount that
// no verb here would write.
func onBothWithFS(t *testing.T, run func(t *testing.T, graph *relation.Graph, fsys vfs.FileSystem)) {
	t.Helper()
	for name, plugin := range plugins {
		t.Run(name, func(t *testing.T) {
			graph, fsys := newGraph(t, plugin)
			run(t, graph, fsys)
		})
	}
}

func edge(from relation.Ref, predicate string, to relation.Ref) relation.Edge {
	return relation.Edge{From: from, Predicate: predicate, To: to}
}

func mustLink(t *testing.T, graph *relation.Graph, s workspace.Scope, e relation.Edge) {
	t.Helper()
	if err := graph.Link(t.Context(), s, e); err != nil {
		t.Fatalf("Link(%+v): %v", e, err)
	}
}

func mustList(t *testing.T, graph *relation.Graph, s workspace.Scope, from relation.Ref) []relation.Edge {
	t.Helper()
	edges, err := graph.List(t.Context(), s, from)
	if err != nil {
		t.Fatalf("List(%+v): %v", from, err)
	}
	return edges
}

func TestLinkAndListRoundTrip(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		// Linked in the order that is not the order they come back in, because
		// what List promises is sorted and not remembered.
		mustLink(t, graph, here, edge(tailor, "uses", letter))
		mustLink(t, graph, here, edge(tailor, "produces", cv))

		want := []relation.Edge{edge(tailor, "produces", cv), edge(tailor, "uses", letter)}
		if got := mustList(t, graph, here, tailor); !slices.Equal(got, want) {
			t.Fatalf("List = %+v, want %+v", got, want)
		}
	})
}

// A subject nobody has linked has no edges, and saying so is not an error: that
// is how a caller finds out a resource stands alone.
func TestListSubjectNothingWasLinkedFrom(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		if got := mustList(t, graph, here, tailor); len(got) != 0 {
			t.Fatalf("List = %+v, want none", got)
		}
	})
}

// An edge is a fact, not a count of how many times it was asserted.
func TestLinkingTheSameEdgeTwiceIsOneEdge(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		mustLink(t, graph, here, edge(tailor, "uses", cv))
		mustLink(t, graph, here, edge(tailor, "uses", cv))

		if got := mustList(t, graph, here, tailor); len(got) != 1 {
			t.Fatalf("List = %+v, want one edge", got)
		}
	})
}

// The predicate is part of the edge. Two things can be related in two ways at
// once, and neither assertion replaces the other.
func TestOnePairCanBeRelatedTwoWays(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		want := []relation.Edge{edge(tailor, "produces", cv), edge(tailor, "uses", cv)}
		for _, e := range want {
			mustLink(t, graph, here, e)
		}

		if got := mustList(t, graph, here, tailor); !slices.Equal(got, want) {
			t.Fatalf("List = %+v, want %+v", got, want)
		}
	})
}

// Curated is the whole point: the graph holds what someone asserted and nothing
// it could have worked out for itself. An extractor is free to symmetrise an
// edge or close it transitively; asserting one here does neither.
func TestNothingIsDerivedFromWhatWasLinked(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		mustLink(t, graph, here, edge(tailor, "uses", cv))
		mustLink(t, graph, here, edge(cv, "uses", letter))

		if got := mustList(t, graph, here, tailor); !slices.Equal(got, []relation.Edge{edge(tailor, "uses", cv)}) {
			t.Fatalf("List of the subject = %+v, want only the edge that was linked", got)
		}
		if got := mustList(t, graph, here, cv); !slices.Equal(got, []relation.Edge{edge(cv, "uses", letter)}) {
			t.Fatalf("List of the object = %+v, want only the edge that was linked from it", got)
		}
		if got := mustList(t, graph, here, letter); len(got) != 0 {
			t.Fatalf("List of the far end = %+v, want none", got)
		}
	})
}

func TestUnlinkRemovesOneEdgeAndLeavesTheRest(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		mustLink(t, graph, here, edge(tailor, "uses", cv))
		mustLink(t, graph, here, edge(tailor, "uses", letter))
		mustLink(t, graph, here, edge(tailor, "produces", cv))

		if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", cv)); err != nil {
			t.Fatalf("Unlink: %v", err)
		}

		want := []relation.Edge{edge(tailor, "produces", cv), edge(tailor, "uses", letter)}
		if got := mustList(t, graph, here, tailor); !slices.Equal(got, want) {
			t.Fatalf("List after Unlink = %+v, want %+v", got, want)
		}
	})
}

// An unlink that failed halfway is completed by running it again, which is only
// true if the second run is not an error.
func TestUnlinkWhatWasNeverLinked(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", cv)); err != nil {
			t.Fatalf("Unlink of nothing: %v", err)
		}

		mustLink(t, graph, here, edge(tailor, "uses", cv))
		if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", cv)); err != nil {
			t.Fatalf("Unlink: %v", err)
		}
		if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", cv)); err != nil {
			t.Fatalf("second Unlink: %v", err)
		}
		if got := mustList(t, graph, here, tailor); len(got) != 0 {
			t.Fatalf("List after Unlink = %+v, want none", got)
		}
	})
}

// The kind is half the address. Two documents of different kinds can share a
// name, and an edge to one of them is not an edge to the other.
func TestKindTellsTwoRefsOfOneNameApart(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		skillCV := relation.Ref{Kind: "skill", Name: "cv"}
		mustLink(t, graph, here, edge(tailor, "uses", cv))
		mustLink(t, graph, here, edge(tailor, "uses", skillCV))

		want := []relation.Edge{edge(tailor, "uses", cv), edge(tailor, "uses", skillCV)}
		if got := mustList(t, graph, here, tailor); !slices.Equal(got, want) {
			t.Fatalf("List = %+v, want %+v", got, want)
		}

		if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", skillCV)); err != nil {
			t.Fatalf("Unlink: %v", err)
		}
		if got := mustList(t, graph, here, tailor); !slices.Equal(got, []relation.Edge{edge(tailor, "uses", cv)}) {
			t.Fatalf("List after unlinking the other kind = %+v, want the blueprint edge", got)
		}
	})
}

// A curated edge is inside the workspace it was asserted in. Two workspaces of
// one user, and two users, are the two ways that has to hold.
func TestScopesHoldTheirOwnEdges(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		mustLink(t, graph, here, edge(tailor, "uses", cv))
		mustLink(t, graph, there, edge(tailor, "uses", letter))

		if got := mustList(t, graph, here, tailor); !slices.Equal(got, []relation.Edge{edge(tailor, "uses", cv)}) {
			t.Fatalf("List in %+v = %+v", here, got)
		}
		if got := mustList(t, graph, there, tailor); !slices.Equal(got, []relation.Edge{edge(tailor, "uses", letter)}) {
			t.Fatalf("List in %+v = %+v", there, got)
		}
		if got := mustList(t, graph, other, tailor); len(got) != 0 {
			t.Fatalf("List in %+v = %+v, want none", other, got)
		}

		if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", cv)); err != nil {
			t.Fatalf("Unlink: %v", err)
		}
		if got := mustList(t, graph, there, tailor); len(got) != 1 {
			t.Fatalf("List in %+v after unlinking in another workspace = %+v, want one edge", there, got)
		}
	})
}

// Withdrawing an edge withdraws the edge. Over a keyspace an edge's key and a
// prefix can share a name, so an unlink that reached under the key would
// withdraw whatever else on the mount is stored beneath it.
func TestUnlinkLeavesWhatIsUnderTheEdge(t *testing.T) {
	graph, fsys := newGraph(t, plugins["keyspace"])
	mustLink(t, graph, here, edge(tailor, "uses", cv))

	beneath, err := here.Path(root, path.Join("skill", "tailor-cv", "uses", "blueprint", "cv", "deeper"))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := fsys.Write(t.Context(), beneath, []byte("kept")); err != nil {
		t.Fatalf("Write(%q): %v", beneath, err)
	}

	if err := graph.Unlink(t.Context(), here, edge(tailor, "uses", cv)); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	got, err := fsys.Read(t.Context(), beneath)
	if err != nil {
		t.Fatalf("Read(%q) after Unlink: %v", beneath, err)
	}
	if !bytes.Equal(got, []byte("kept")) {
		t.Fatalf("Read(%q) = %q, want %q", beneath, got, "kept")
	}
}

// The tree under a mount is a tree. Something else standing where a predicate
// belongs is not a broken edge, and List answers with the edges rather than
// failing over it.
func TestListPassesOverWhatIsNotAnEdge(t *testing.T) {
	onBothWithFS(t, func(t *testing.T, graph *relation.Graph, fsys vfs.FileSystem) {
		mustLink(t, graph, here, edge(tailor, "uses", cv))

		stray, err := here.Path(root, path.Join("skill", "tailor-cv", "notes"))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if err := fsys.Write(t.Context(), stray, []byte("not an edge")); err != nil {
			t.Fatalf("Write(%q): %v", stray, err)
		}

		if got := mustList(t, graph, here, tailor); !slices.Equal(got, []relation.Edge{edge(tailor, "uses", cv)}) {
			t.Fatalf("List = %+v, want the one edge", got)
		}
	})
}

// A ref and a predicate are one flat segment each, for the reason a scope's
// segments are: a name carrying a separator names a place inside another one,
// and a name that resolves away lands on an edge nobody asserted.
func TestRefusesNamesThatAreNotOneSegment(t *testing.T) {
	badRefs := map[string]relation.Ref{
		"empty kind":        {Name: "cv"},
		"empty name":        {Kind: "blueprint"},
		"kind separator":    {Kind: "a/b", Name: "cv"},
		"name separator":    {Kind: "blueprint", Name: "a/b"},
		"kind is the dot":   {Kind: ".", Name: "cv"},
		"name climbs":       {Kind: "blueprint", Name: ".."},
		"name climbs later": {Kind: "blueprint", Name: "cv/.."},
	}
	badPredicates := map[string]string{
		"empty":     "",
		"separator": "uses/deeper",
		"climbs":    "..",
		"the dot":   ".",
	}

	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		for name, bad := range badRefs {
			t.Run("subject "+name, func(t *testing.T) {
				refuses(t, graph, edge(bad, "uses", cv))

				if _, err := graph.List(t.Context(), here, bad); !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("List = %v, want fs.ErrInvalid", err)
				}
			})
			t.Run("object "+name, func(t *testing.T) { refuses(t, graph, edge(tailor, "uses", bad)) })
		}
		for name, bad := range badPredicates {
			t.Run("predicate "+name, func(t *testing.T) { refuses(t, graph, edge(tailor, bad, cv)) })
		}
	})
}

// refuses asserts that neither verb taking a whole edge accepts this one.
func refuses(t *testing.T, graph *relation.Graph, e relation.Edge) {
	t.Helper()
	if err := graph.Link(t.Context(), here, e); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Link = %v, want fs.ErrInvalid", err)
	}
	if err := graph.Unlink(t.Context(), here, e); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Unlink = %v, want fs.ErrInvalid", err)
	}
}

// A scope naming no workspace is the subtree holding every workspace the user
// has. An edge written there would stand where a workspace belongs and be
// listed as one.
func TestRefusesUserScope(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		user := workspace.Scope{User: "ada"}

		if err := graph.Link(t.Context(), user, edge(tailor, "uses", cv)); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Link = %v, want fs.ErrInvalid", err)
		}
		if err := graph.Unlink(t.Context(), user, edge(tailor, "uses", cv)); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Unlink = %v, want fs.ErrInvalid", err)
		}
		if _, err := graph.List(t.Context(), user, tailor); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("List = %v, want fs.ErrInvalid", err)
		}
	})
}

// A scope that cannot derive a path is refused by the scope, and the verbs
// report what it said rather than writing somewhere else.
func TestRefusesScopeThatIsNotFlat(t *testing.T) {
	onBoth(t, func(t *testing.T, graph *relation.Graph) {
		bad := workspace.Scope{User: "ada/grace", Workspace: "hunt"}

		if err := graph.Link(t.Context(), bad, edge(tailor, "uses", cv)); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Link = %v, want fs.ErrInvalid", err)
		}
	})
}

// The root is where a mount was put, so it is the mount table's rule for a
// prefix. A graph over a root nothing can be mounted at is a startup failure.
func TestNewGraphRefusesARootThatIsNotAPrefix(t *testing.T) {
	for name, bad := range map[string]string{
		"empty":    "",
		"relative": "relations",
		"unclean":  "/relations/",
		"climbing": "/relations/../relations",
		"dot":      "/relations/.",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := relation.NewGraph(vfs.NewMountTable(), bad); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("NewGraph(%q) = %v, want fs.ErrInvalid", bad, err)
			}
		})
	}
}
