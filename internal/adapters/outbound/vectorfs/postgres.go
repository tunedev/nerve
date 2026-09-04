package vectorfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the corpus the platform ships: documents and their vectors in one
// row of one table, with pgvector doing the comparing.
//
// The document and its vector are columns of the same row, written by the same
// statement. That is what makes "in the corpus" and "findable" the same fact:
// no failure exists that leaves a document stored and unsearchable, or a vector
// pointing at text that is no longer there.
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

// OpenPostgres returns the corpus named name, held in the database at url with
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

// schema is what a database with this index not in it becomes.
//
// The embedding is nullable because a document with no text has no vector, and
// a zero vector in its place would be a document equally close to every
// question. The index skips the nulls, so an empty document costs a row and
// never an answer.
func schema(table string, dimension int) string {
	ddl := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	key        TEXT PRIMARY KEY,
	body       BYTEA       NOT NULL,
	mode       INTEGER     NOT NULL,
	embedding  vector(%d),
	written_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`, table, dimension),
	}
	if dimension <= hnswLimit {
		ddl = append(ddl, fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s_nearest ON %s USING hnsw (embedding vector_cosine_ops)`,
			table, table))
	}
	return strings.Join(ddl, ";\n") + ";"
}

// Close gives back the connections. Nothing in the port closes a plugin, so
// this is for a caller that owns the corpus's lifetime and not for the tree.
func (p *Postgres) Close() { p.pool.Close() }

// Put stores body at key with the vector made from it, replacing what was
// there. Both columns are written by the one statement, so a document is never
// stored beside the vector of an older version of itself.
func (p *Postgres) Put(ctx context.Context, key string, body []byte, vector []float32, mode fs.FileMode) error {
	put := fmt.Sprintf(`
INSERT INTO %s (key, body, mode, embedding, written_at)
VALUES ($1, $2, $3, $4::vector, now())
ON CONFLICT (key) DO UPDATE
SET body = EXCLUDED.body, mode = EXCLUDED.mode, embedding = EXCLUDED.embedding, written_at = now()
`, p.table)

	// A nil slice is empty content, and the driver would send it as NULL: an
	// empty document and no document are the same value in Go and different
	// rows in Postgres. Translating here is the same move as turning the
	// driver's empty result into fs.ErrNotExist.
	if body == nil {
		body = []byte{}
	}
	if _, err := p.pool.Exec(ctx, put, key, body, int32(mode.Perm()), literal(vector)); err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	return nil
}

// Get returns the document stored at key.
func (p *Postgres) Get(ctx context.Context, key string) ([]byte, error) {
	get := fmt.Sprintf(`SELECT body FROM %s WHERE key = $1`, p.table)

	var body []byte
	if err := p.pool.QueryRow(ctx, get, key).Scan(&body); err != nil {
		return nil, p.missing("get", key, err)
	}
	return body, nil
}

// Peek describes the document at key without hauling its body back.
func (p *Postgres) Peek(ctx context.Context, key string) (Entry, error) {
	peek := fmt.Sprintf(`SELECT key, octet_length(body), mode, written_at FROM %s WHERE key = $1`, p.table)

	entry, err := scan(p.pool.QueryRow(ctx, peek, key))
	if err != nil {
		return Entry{}, p.missing("peek", key, err)
	}
	return entry, nil
}

// List describes every document under prefix, however deep.
func (p *Postgres) List(ctx context.Context, prefix string) ([]Entry, error) {
	list := fmt.Sprintf(`
SELECT key, octet_length(body), mode, written_at FROM %s
WHERE starts_with(key, $1)
ORDER BY key
`, p.table)

	rows, err := p.pool.Query(ctx, list, prefix)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", prefix, err)
	}
	defer rows.Close()

	var found []Entry
	for rows.Next() {
		entry, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", prefix, err)
		}
		found = append(found, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %q: %w", prefix, err)
	}
	return found, nil
}

// Drop removes the document at key, and its vector with it.
func (p *Postgres) Drop(ctx context.Context, key string) error {
	drop := fmt.Sprintf(`DELETE FROM %s WHERE key = $1`, p.table)

	tag, err := p.pool.Exec(ctx, drop, key)
	if err != nil {
		return fmt.Errorf("drop %q: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("drop %q: %w", key, fs.ErrNotExist)
	}
	return nil
}

// DropAll removes every document under prefix.
func (p *Postgres) DropAll(ctx context.Context, prefix string) error {
	dropAll := fmt.Sprintf(`DELETE FROM %s WHERE starts_with(key, $1)`, p.table)

	if _, err := p.pool.Exec(ctx, dropAll, prefix); err != nil {
		return fmt.Errorf("drop_all %q: %w", prefix, err)
	}
	return nil
}

// Move puts the document at from at to, vector and all.
//
// Both statements are one transaction, because the landing has to be gone
// before the move lands on it and a move that failed in between would leave the
// corpus with neither document where the caller asked for it.
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
	tag, err := tx.Exec(ctx, move, from, to)
	if err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("move %q: %w", from, fs.ErrNotExist)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	return nil
}

// Chmod sets the permission bits the document at key carries.
func (p *Postgres) Chmod(ctx context.Context, key string, mode fs.FileMode) error {
	chmod := fmt.Sprintf(`UPDATE %s SET mode = $2 WHERE key = $1`, p.table)

	tag, err := p.pool.Exec(ctx, chmod, key, int32(mode.Perm()))
	if err != nil {
		return fmt.Errorf("chmod %q: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("chmod %q: %w", key, fs.ErrNotExist)
	}
	return nil
}

// Nearest returns the documents closest to vector by cosine distance, closest
// first.
//
// The distance operator appears in the ordering as well as in the projection,
// because that is what an HNSW index answers: a query that ordered by anything
// else would read the whole table and sort it. The score is turned into a
// similarity so that larger is closer, which is the order the port promises and
// the opposite of what a distance says.
func (p *Postgres) Nearest(ctx context.Context, vector []float32, limit int) ([]Hit, error) {
	nearest := fmt.Sprintf(`
SELECT key, 1 - (embedding <=> $1::vector) FROM %s
WHERE embedding IS NOT NULL
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

// row is what a describing query hands back, so one scan serves both the single
// lookup and the listing.
type row interface {
	Scan(dest ...any) error
}

func scan(from row) (Entry, error) {
	var entry Entry
	var mode int32
	if err := from.Scan(&entry.Key, &entry.Size, &mode, &entry.Written); err != nil {
		return Entry{}, err
	}
	entry.Mode = fs.FileMode(mode) //nolint:gosec // the column holds what fs.FileMode.Perm was written as
	return entry, nil
}

// literal spells a vector the way pgvector reads one, or nil for a document
// with nothing to compare. It is text with a cast rather than a registered
// type, so nothing in the build depends on a driver extension to store a
// vector.
func literal(vector []float32) *string {
	if vector == nil {
		return nil
	}

	spelled := strings.Builder{}
	spelled.WriteByte('[')
	for i, part := range vector {
		if i > 0 {
			spelled.WriteByte(',')
		}
		spelled.WriteString(strconv.FormatFloat(float64(part), 'f', -1, 32))
	}
	spelled.WriteByte(']')

	text := spelled.String()
	return &text
}

// missing turns the driver's empty result into the sentinel the port promises.
// Classifying at the plugin's edge is what keeps io/fs out of the driver and
// the driver out of everything above.
func (p *Postgres) missing(verb, key string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s %q: %w", verb, key, fs.ErrNotExist)
	}
	return fmt.Errorf("%s %q: %w", verb, key, err)
}
