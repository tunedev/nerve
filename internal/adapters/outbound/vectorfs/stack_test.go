package vectorfs_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// uniqueIndex names each case's own table, because a corpus that outlives the
// process cannot be emptied by making a new one.
var uniqueIndex atomic.Int64

// indexEnv turns this file on. Nothing here runs without a database to talk to,
// which is what keeps the rest of the package's tests true on a plane: the
// semantics are asserted against a corpus in a map, and this asserts that the
// shipped side keeps the same promises.
//
// `make up` starts what this points at.
const indexEnv = "NERVE_VECTORFS_INDEX"

// The same suite as the offline run, against pgvector. Each case gets an index
// of its own, because "a corpus with nothing in it" has to mean that when the
// corpus outlives the process.
func TestConformanceOfTheCorpusAgainstPgvector(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: stackIndex, Root: "/" + vectorfs.Docs})
}

func TestConformanceOfTheMountShapeAgainstPgvector(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: stackIndex, Root: "/"})
}

// The acceptance against pgvector: documents written through the tree come back
// ranked against a question read through it. The ordering is the database's,
// through an HNSW index over cosine distance, and it agrees with the one the
// offline suite asserts.
func TestPgvectorAnswersAQuestionWithTheCorpusRanked(t *testing.T) {
	fsys := stackIndex(t)
	ctx := t.Context()

	corpus := map[string]string{
		"retry":   "a retry repeats a call that failed for a reason that may pass",
		"breaker": "a circuit breaker stops calling a dependency that keeps failing",
		"pasta":   "boil the water before the pasta goes in",
	}
	for key, text := range corpus {
		if err := fsys.Write(ctx, "/docs/"+key, []byte(text)); err != nil {
			t.Fatalf("Write(/docs/%s) = %v", key, err)
		}
	}

	answered, err := fsys.Read(ctx, "/query/a retry repeats a call that failed")
	if err != nil {
		t.Fatalf("Read(/query/...) = %v", err)
	}

	ranked := rankedKeys(t, answered)
	if len(ranked) != len(corpus) {
		t.Fatalf("pgvector ranked %v, want all three documents", ranked)
	}
	if ranked[0] != "retry" {
		t.Errorf("the closest document was %q, want %q", ranked[0], "retry")
	}
	if last := ranked[len(ranked)-1]; last != "pasta" {
		t.Errorf("the furthest document was %q, want %q", last, "pasta")
	}
}

// A document that was removed is gone from the ranking as well as from the
// corpus. The vector is a column of the row, so nothing has to remember to
// delete it separately.
func TestPgvectorForgetsARemovedDocument(t *testing.T) {
	fsys := stackIndex(t)
	ctx := t.Context()

	if err := fsys.Write(ctx, "/docs/gone", []byte("a call that failed")); err != nil {
		t.Fatalf("Write(/docs/gone) = %v", err)
	}
	if err := fsys.Remove(ctx, "/docs/gone"); err != nil {
		t.Fatalf("Remove(/docs/gone) = %v", err)
	}

	answered, err := fsys.Read(ctx, "/query/a call that failed")
	if err != nil {
		t.Fatalf("Read(/query/...) = %v", err)
	}
	if ranked := rankedKeys(t, answered); len(ranked) != 0 {
		t.Errorf("a removed document still ranked as %v", ranked)
	}
}

// stackIndex returns an index of its own in the database the environment names,
// embedded by the bag of words so the case asserts pgvector's ordering rather
// than a model's.
func stackIndex(t *testing.T) vfs.FileSystem {
	t.Helper()

	url := os.Getenv(indexEnv)
	if url == "" {
		t.Skipf("%s is not set; `make up` starts what it names", indexEnv)
	}

	name := fmt.Sprintf("case_%d", uniqueIndex.Add(1))
	index, err := vectorfs.OpenPostgres(url, name, width)
	if err != nil {
		t.Fatalf("OpenPostgres(%s) = %v", name, err)
	}
	t.Cleanup(func() {
		drop(t, url, "vector_"+name)
		index.Close()
	})
	return vectorfs.New(name, width, neighbours, index, bagEmbedder{width: width})
}

// drop takes a case's table away again, so a run leaves the database as it
// found it. A case that shared one table with the next would be asserting the
// order of a corpus somebody else wrote.
func drop(t *testing.T, url, table string) {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Errorf("dropping %s: %v", table, err)
		return
	}
	defer pool.Close()

	if _, err := pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table); err != nil {
		t.Errorf("dropping %s: %v", table, err)
	}
}
