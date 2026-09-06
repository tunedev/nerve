package graphfs

import (
	"slices"
	"strconv"
	"strings"

	"github.com/tunedev/nerve/internal/core/llm"
)

// heading names the columns an expansion carries, so what a reader gets is
// self-describing rather than five columns in an order it has to know.
const heading = "from\tfrom_kind\tpredicate\tto\tto_kind\tsupport\n"

// claim is one edge of the graph and how much of the corpus stands behind it.
//
// The two entities are the port's pairs rather than bare names, because that is
// what a node is: "Postgres" the product and "Postgres" the person are two
// things, and a graph that merged them would answer about neither.
type claim struct {
	edge    llm.Relation
	support int
}

// expand answers with every claim one hop out of the entities named name,
// whatever kind of thing they are.
//
// Matching is by name alone. A caller has a name — it read one out of a
// document, or a person typed one — and Kind is the extractor's vocabulary,
// which the caller has no way to know and this package no business inventing.
// Two kinds sharing a name stay two nodes in the answer, where the kind columns
// tell them apart.
//
// A relation stated by two documents is one claim standing on two, not two
// edges. That is what makes this a graph rather than a log of extractions, and
// it is what gives the answer an order worth truncating: support first, so the
// claims a corpus keeps repeating survive the bound, then the neighbour's name
// and the predicate, so the rest of the order is the same on every run.
//
// A name nothing says anything about answers with the heading and no rows. That
// is an answer — the graph was asked and holds nothing — rather than a missing
// path, which a caller would otherwise have to distinguish an unrelated entity
// from an unknown one by.
func (f *FS) expand(name string) []byte {
	claims := f.claimsAbout(name)
	slices.SortFunc(claims, func(a, b claim) int { return order(name, a, b) })
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
		report.WriteByte('\n')
	}
	return []byte(report.String())
}

// claimsAbout gathers the distinct relations naming an entity called name at
// either end, counting how many documents stated each.
//
// It walks every document rather than an adjacency index. The documents are the
// record, and an index beside them is a second thing to keep true across every
// write, rename and removal; a graph this holds whole in memory is one a walk
// answers from, and a graph too big for that is what a graph backend is for.
func (f *FS) claimsAbout(name string) []claim {
	standing := map[llm.Relation]int{}
	f.read(func(documents map[string]document) {
		for _, held := range documents {
			for _, stated := range distinct(held.Read.Relations) {
				if stated.From.Name == name || stated.To.Name == name {
					standing[stated]++
				}
			}
		}
	})

	claims := make([]claim, 0, len(standing))
	for edge, support := range standing {
		claims = append(claims, claim{edge: edge, support: support})
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

// order ranks two claims about the entity called name: the better supported
// first, then by the neighbour each names, then by what is said about it. Every
// key after the first is there so that two runs over one graph answer
// identically.
func order(name string, a, b claim) int {
	if a.support != b.support {
		return b.support - a.support
	}
	if by := strings.Compare(neighbour(name, a.edge), neighbour(name, b.edge)); by != 0 {
		return by
	}
	if by := strings.Compare(a.edge.Predicate, b.edge.Predicate); by != 0 {
		return by
	}
	return strings.Compare(a.edge.From.Name+a.edge.From.Kind, b.edge.From.Name+b.edge.From.Kind)
}

// neighbour is the end of an edge that is not the entity asked about, which is
// the thing one hop away. An edge from a name to itself has the same name at
// both ends and is its own neighbour.
func neighbour(name string, edge llm.Relation) string {
	if edge.From.Name == name {
		return edge.To.Name
	}
	return edge.From.Name
}
