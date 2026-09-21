package graphfs

import (
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// heading names the columns an expansion carries, so what a reader gets is
// self-describing rather than five columns in an order it has to know.
const heading = "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\thop\n"

// claim is one edge of the graph, how much of the corpus stands behind it, and
// where a walk reached it: the hop it was first found at and the end it was
// found from.
//
// The two entities are the port's pairs rather than bare names, because that is
// what a node is: "Postgres" the product and "Postgres" the person are two
// things, and a graph that merged them would answer about neither.
type claim struct {
	edge    llm.Relation
	support int
	hop     int
	near    string
}

// expand answers with every claim within the mount's depth of the entities
// named name, whatever kind of thing they are.
//
// Matching is by name alone. A caller has a name — it read one out of a
// document, or a person typed one — and Kind is the extractor's vocabulary,
// which the caller has no way to know and this package no business inventing.
// Two kinds sharing a name stay two nodes in the answer, where the kind columns
// tell them apart. A walk matches by name at every hop too, so a name two kinds
// share joins both neighbourhoods beyond it.
//
// A relation stated by two documents is one claim standing on two, not two
// edges. That is what makes this a graph rather than a log of extractions, and
// it is what gives the answer an order worth truncating: support first, so the
// claims a corpus keeps repeating survive the bound, then the nearer hop, then
// the neighbour's name and the predicate, so the rest of the order is the same
// on every run.
//
// The bound is applied once, to the answer. The walk gathers every claim
// within depth before any is cut, so a well-supported claim beyond a weakly
// supported neighbour survives, where a walk that cut each hop to the bound
// would never have followed that neighbour.
//
// A name nothing says anything about answers with the heading and no rows. That
// is an answer — the graph was asked and holds nothing — rather than a missing
// path, which a caller would otherwise have to distinguish an unrelated entity
// from an unknown one by.
func (f *FS) expand(name string) []byte {
	claims := f.walk(name)
	slices.SortFunc(claims, order)
	if len(claims) > f.edges {
		claims = claims[:f.edges]
	}

	report := strings.Builder{}
	report.WriteString(heading)
	for _, one := range claims {
		report.WriteString(one.edge.From.Name)
		report.WriteByte('\t')
		report.WriteString(one.edge.From.Kind)
		report.WriteByte('\t')
		report.WriteString(one.edge.Predicate)
		report.WriteByte('\t')
		report.WriteString(one.edge.To.Name)
		report.WriteByte('\t')
		report.WriteString(one.edge.To.Kind)
		report.WriteByte('\t')
		report.WriteString(strconv.Itoa(one.support))
		report.WriteByte('\t')
		report.WriteString(strconv.Itoa(one.hop))
		report.WriteByte('\n')
	}
	return []byte(report.String())
}

// walk is a breadth-first walk out of the entities named name, depth hops
// deep. Each hop takes the claims naming an entity the last hop reached, and a
// claim already taken at a nearer hop is not taken again, so the edge back to
// where a walk came from is the edge it came along.
func (f *FS) walk(name string) []claim {
	reached := map[string]bool{name: true}
	frontier := map[string]bool{name: true}
	taken := map[llm.Relation]bool{}

	var found []claim
	for hop := 1; hop <= f.depth && len(frontier) > 0; hop++ {
		next := map[string]bool{}
		for _, one := range f.claimsAbout(frontier) {
			if taken[one.edge] {
				continue
			}
			taken[one.edge] = true
			one.hop = hop
			found = append(found, one)

			if far := neighbour(one.near, one.edge); !reached[far] {
				reached[far] = true
				next[far] = true
			}
		}
		frontier = next
	}
	return found
}

// claimsAbout gathers the distinct relations naming an entity in names at
// either end, counting how many documents stated each.
//
// It walks every document rather than an adjacency index. The documents are the
// record, and an index beside them is a second thing to keep true across every
// write, rename and removal; a graph this holds whole in memory is one a walk
// answers from, and a graph too big for that is what a graph backend is for.
func (f *FS) claimsAbout(names map[string]bool) []claim {
	standing := map[llm.Relation]int{}
	f.read(func(documents map[string]document) {
		for _, held := range documents {
			for _, stated := range distinct(held.Read.Relations) {
				if names[stated.From.Name] || names[stated.To.Name] {
					standing[stated]++
				}
			}
		}
	})

	claims := make([]claim, 0, len(standing))
	for edge, support := range standing {
		near := edge.From.Name
		if !names[near] {
			near = edge.To.Name
		}
		claims = append(claims, claim{edge: edge, support: support, near: near})
	}
	return claims
}

// distinct is the set of relations one document stated. A document that made
// the same claim twice stated it once: support counts the corpus that stands
// behind an edge, and a sentence repeated in one text is still one text.
func distinct(stated []llm.Relation) []llm.Relation {
	once := make([]llm.Relation, 0, len(stated))
	seen := make(map[llm.Relation]struct{}, len(stated))
	for _, one := range stated {
		if _, already := seen[one]; already {
			continue
		}
		seen[one] = struct{}{}
		once = append(once, one)
	}
	return once
}

// order ranks two claims a walk found: the better supported first, then the
// nearer, then by the neighbour each names, then by what is said about it.
// Every key after the first is there so that two runs over one graph answer
// identically, which is why both ends are compared last: two claims can differ
// only in the kind at one end.
func order(a, b claim) int {
	if a.support != b.support {
		return b.support - a.support
	}
	if a.hop != b.hop {
		return a.hop - b.hop
	}
	if by := strings.Compare(neighbour(a.near, a.edge), neighbour(b.near, b.edge)); by != 0 {
		return by
	}
	if by := strings.Compare(a.edge.Predicate, b.edge.Predicate); by != 0 {
		return by
	}
	if by := strings.Compare(a.edge.From.Name+a.edge.From.Kind, b.edge.From.Name+b.edge.From.Kind); by != 0 {
		return by
	}
	return strings.Compare(a.edge.To.Name+a.edge.To.Kind, b.edge.To.Name+b.edge.To.Kind)
}

// neighbour is the end of an edge that is not the entity called name, which is
// the thing one hop further out. An edge from a name to itself has the same name at
// both ends and is its own neighbour.
func neighbour(name string, edge llm.Relation) string {
	if edge.From.Name == name {
		return edge.To.Name
	}
	return edge.From.Name
}
