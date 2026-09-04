package vectorfs

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the ranking the platform ships: a vector per key, with pgvector
// doing the comparing. The documents those keys name are the DocStore's, and
// nothing here reads one.
//
// One table holds one index, named after it, because the width of a vector is
// part of the column's type. Two indexes over two models have two widths and
// cannot share a column; giving each its own table is what lets a build hold
// both without either lying about the other.
type Postgres struct {
	pool  *pgxpool.Pool
	table string
}

// dialTimeout bounds the one call that happens before there is a caller to
// carry a deadline: reaching the database at composition to put the table
// there.
const dialTimeout = 10 * time.Second

// hnswLimit is how wide a vector pgvector will build an HNSW index over. A
// wider column is legal and searched exactly rather than approximately, which
// is slower and no less correct, so it is a mount that works rather than a
// mount that refuses to boot.
const hnswLimit = 2000

// vectorLimit is the widest vector pgvector stores.
const vectorLimit = 16000

// naming is what an index may be called. The name becomes an identifier in DDL
// that no parameter can carry, so it is checked rather than escaped: a name
// that is not a bare lowercase word is refused before any statement is built
// from it.
var naming = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// OpenPostgres returns the ranking named name, held in the database at url with
// vectors of dimension, creating the table and its index when they are not
// there yet.
//
// It reaches the database now rather than on the first write, so a mount that
// cannot store anything is a boot failure an operator can still act on instead
// of a path that fails later under load.
func OpenPostgres(url, name string, dimension int) (*Postgres, error) {
	if !naming.MatchString(name) {
		return nil, fmt.Errorf("index %q is not a name a table can be called, which is %s: %w",
			name, naming, fs.ErrInvalid)
	}
	if dimension < 1 || dimension > vectorLimit {
		return nil, fmt.Errorf("index %q holds vectors of %d, and a vector is 1 to %d wide: %w",
			name, dimension, vectorLimit, fs.ErrInvalid)
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		return nil, fmt.Errorf("index %q: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	table := "vector_" + name
	if _, err := pool.Exec(ctx, schema(table, dimension)); err != nil {
		pool.Close()
		return nil, fmt.Errorf("index %q: %w", name, err)
	}
	return &Postgres{pool: pool, table: table}, nil
}

// schema is the table this index needs, stated rather than stepped through: a
// row is a key and the vector made from the document filed under it, and the
// document itself is somewhere else. That is what lets a corpus be a bucket
// while its ranking is a database.
//
// A table this index left behind when it also held the documents converges to
// the same shape: the three columns that were the document go, the rows with no
// vector go with them, and what is left is what the create above would have
// made. Every statement is a no-op on a table already in that shape, so running
// this a second time proves the first.
func schema(table string, dimension int) string {
	ddl := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	key       TEXT PRIMARY KEY,
	embedding vector(%d) NOT NULL
)`, table, dimension),
		fmt.Sprintf(`ALTER TABLE %s DROP COLUMN IF EXISTS body`, table),
		fmt.Sprintf(`ALTER TABLE %s DROP COLUMN IF EXISTS mode`, table),
		fmt.Sprintf(`ALTER TABLE %s DROP COLUMN IF EXISTS written_at`, table),
		fmt.Sprintf(`DELETE FROM %s WHERE embedding IS NULL`, table),
		fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN embedding SET NOT NULL`, table),
	}
	if dimension <= hnswLimit {
		ddl = append(ddl, fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s_nearest ON %s USING hnsw (embedding vector_cosine_ops)`,
			table, table))
	}
	return strings.Join(ddl, ";\n") + ";"
}

// Close gives back the connections. Nothing in the port closes a plugin, so
// this is for a caller that owns the index's lifetime and not for the tree.
func (p *Postgres) Close() { p.pool.Close() }

// Put files vector under key, replacing what was there. A nil vector is a
// document with nothing to compare, and it is taken out of the table rather
// than stored as a null: a row that can never be an answer is a row a query has
// to keep skipping.
func (p *Postgres) Put(ctx context.Context, key string, vector []float32) error {
	if vector == nil {
		return p.Drop(ctx, key)
	}

	put := fmt.Sprintf(`
INSERT INTO %s (key, embedding) VALUES ($1, $2::vector)
ON CONFLICT (key) DO UPDATE SET embedding = EXCLUDED.embedding
`, p.table)

	if _, err := p.pool.Exec(ctx, put, key, literal(vector)); err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	return nil
}

// Drop takes key out of the ranking. A key with no vector is not an error: the
// store is what says whether a document is there.
func (p *Postgres) Drop(ctx context.Context, key string) error {
	drop := fmt.Sprintf(`DELETE FROM %s WHERE key = $1`, p.table)

	if _, err := p.pool.Exec(ctx, drop, key); err != nil {
		return fmt.Errorf("drop %q: %w", key, err)
	}
	return nil
}

// DropAll takes every key under prefix out of the ranking.
func (p *Postgres) DropAll(ctx context.Context, prefix string) error {
	dropAll := fmt.Sprintf(`DELETE FROM %s WHERE starts_with(key, $1)`, p.table)

	if _, err := p.pool.Exec(ctx, dropAll, prefix); err != nil {
		return fmt.Errorf("drop_all %q: %w", prefix, err)
	}
	return nil
}

// Move files the vector at from under to instead.
//
// Both statements are one transaction, because the landing has to be gone
// before the move lands on it and a move that failed in between would leave the
// ranking with neither key.
func (p *Postgres) Move(ctx context.Context, from, to string) error {
	clear := fmt.Sprintf(`DELETE FROM %s WHERE key = $1`, p.table)
	move := fmt.Sprintf(`UPDATE %s SET key = $2 WHERE key = $1`, p.table)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("move %q: %w", from, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, clear, to); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	if _, err := tx.Exec(ctx, move, from, to); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	return nil
}

// Nearest returns the keys closest to vector by cosine distance, closest first.
//
// The distance operator appears in the ordering as well as in the projection,
// because that is what an HNSW index answers: a query that ordered by anything
// else would read the whole table and sort it. The score is turned into a
// similarity so that larger is closer, which is the order the port promises and
// the opposite of what a distance says.
func (p *Postgres) Nearest(ctx context.Context, vector []float32, limit int) ([]Hit, error) {
	nearest := fmt.Sprintf(`
SELECT key, 1 - (embedding <=> $1::vector) FROM %s
ORDER BY embedding <=> $1::vector
LIMIT $2
`, p.table)

	rows, err := p.pool.Query(ctx, nearest, literal(vector), limit)
	if err != nil {
		return nil, fmt.Errorf("nearest: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var hit Hit
		if err := rows.Scan(&hit.Key, &hit.Score); err != nil {
			return nil, fmt.Errorf("nearest: %w", err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nearest: %w", err)
	}
	return hits, nil
}

// literal spells a vector the way pgvector reads one. It is text with a cast
// rather than a registered type, so nothing in the build depends on a driver
// extension to store a vector.
func literal(vector []float32) string {
	spelled := strings.Builder{}
	spelled.WriteByte('[')
	for i, part := range vector {
		if i > 0 {
			spelled.WriteByte(',')
		}
		spelled.WriteString(strconv.FormatFloat(float64(part), 'f', -1, 32))
	}
	spelled.WriteByte(']')
	return spelled.String()
}
