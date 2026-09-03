// Package search is how a caller finds what it cannot name.
//
// Every route takes a URI prefix, and the prefix is the whole of the routing.
// A memory, an ingested document and a skill are three mounts, and one Find
// serves all three because it never learns which one answered: it works in the
// tree's vocabulary — paths and bytes — and what a path means under a prefix is
// that mount's business rather than this package's.
//
// The routes store nothing and index nothing. They read the one FileSystem the
// mount table presents, through the view the scope derives, so a result comes
// back in the caller's own vocabulary and the layout that produced it is never
// spelled out to anyone.
//
// Ranking sits behind a port for the reason the library's does: term overlap
// over the entries on the laptop today, an embedding or fused ranker over the
// retrieval layer once there is one, and the routes cannot tell them apart.
//
// A route that reads content refuses a path where reading consumes it. A queue
// job is gone once it has been read, so a grep across a prefix holding one
// would answer a question by destroying its subject; the mount says so per path
// and the routes believe it.
package search

import "context"

// Entry is one candidate a ranker is handed: where it is and what it says.
type Entry struct {
	// Path names the entry inside the prefix, in the caller's vocabulary.
	Path string
	// Text is the entry's content.
	Text string
}

// Hit is one entry a route matched. Scores are comparable within one call and
// mean nothing between two: a ranker scores however it likes as long as the
// order is right.
type Hit struct {
	Path  string
	Score float64
}

// Line is one matching line of one entry, numbered from one the way a reader
// counts them rather than the way a slice does.
type Line struct {
	Path   string
	Number int
	Text   string
}

// Ranker orders entries against what a caller is looking for.
//
// It returns only the entries it finds relevant, best first. A ranker that
// finds nothing relevant returns no hits rather than everything in score order:
// "these are the entries for this question" and "here is the prefix, ranked"
// are different answers, and only the first is worth a caller's context window.
type Ranker interface {
	Rank(ctx context.Context, query string, entries []Entry) ([]Hit, error)
}
