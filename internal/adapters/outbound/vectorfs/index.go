package vectorfs

import "context"

// Index is where the vectors live and the one query that compares them to each
// other. pgvector is the shipped side and whatever else speaks these verbs is
// another, which is what keeps the semantics above this line assertable with
// nothing started.
//
// It holds no documents. Those are the DocStore's, and the split is what makes
// the corpus a corpus rather than a column: a document is worth keeping on its
// own, and a vector is a derived thing that a different model would make
// differently. It also means the two can be different kinds of storage, which
// is the point — a bucket full of documents and a database of vectors over
// them.
//
// A key is the same flat name the DocStore filed the document under. Nothing
// here reads a document or knows one exists.
//
// Because the store is the record and the index is derived, a key the index has
// no vector for is not an error to drop or to move. Only the store can say
// whether a document is there.
type Index interface {
	// Put files vector under key, replacing whatever was there. A nil vector
	// takes the key out of the ranking: a document with nothing in it has no
	// similarity to anything, so there is nothing for Nearest to return.
	Put(ctx context.Context, key string, vector []float32) error

	// Drop takes key out of the ranking. A key with no vector is not an error.
	Drop(ctx context.Context, key string) error

	// DropAll takes every key starting with prefix out of the ranking.
	DropAll(ctx context.Context, prefix string) error

	// Move files the vector at from under to instead, replacing what is at to.
	// The vector travels unchanged: a rename changes a name and not what the
	// document says, so nothing is embedded again. A key with no vector is not
	// an error.
	Move(ctx context.Context, from, to string) error

	// Nearest returns up to limit keys closest to vector, closest first. An
	// index with nothing comparable in it returns no hits, which is an answer
	// and not a failure.
	Nearest(ctx context.Context, vector []float32, limit int) ([]Hit, error)
}

// Hit is one document a query matched: where it is and how close it was.
//
// Scores are comparable within one query and mean nothing between two, which is
// the same promise search.Hit makes. Larger is closer, so the order a caller
// reads is the order the slice is already in.
type Hit struct {
	Key   string
	Score float64
}
