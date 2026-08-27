package queuefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the journal the platform ships: the system of record a queue's
// messages survive in, and the outbox the relay reads.
//
// One row is one message, and that is what makes the outbox transactional
// without a second table. Recording a message and recording that the broker has
// not been told about it are columns of the same row, written by the same
// statement, so no failure exists that leaves one without the other.
//
// A row outlives the claim that took it out of the queue. Every verb that
// addresses a message addresses a pending one, and the rows behind them are
// what a consumer arriving later is backfilled from — the broker keeps nothing,
// so this has to.
//
// One table holds every queue. A mount is scoped to its own name, which is why
// nothing in the port carries one.
type Postgres struct {
	pool  *pgxpool.Pool
	queue string
}

// schema is what a database with no queue in it becomes. The partial unique
// index is the rule that a key addresses one message: at most one row under a
// name is ever pending, and a second write to that name replaces it rather than
// queueing beside it.
const schema = `
CREATE TABLE IF NOT EXISTS queue_messages (
	id          BIGSERIAL PRIMARY KEY,
	queue       TEXT        NOT NULL,
	key         TEXT        NOT NULL,
	body        BYTEA       NOT NULL,
	mode        INTEGER     NOT NULL,
	recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	carried_at  TIMESTAMPTZ,
	claimed_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS queue_messages_pending
	ON queue_messages (queue, key) WHERE claimed_at IS NULL;
CREATE INDEX IF NOT EXISTS queue_messages_uncarried
	ON queue_messages (id) WHERE carried_at IS NULL;
`

// dialTimeout bounds the one call that happens before there is a caller to
// carry a deadline: reaching the database at composition to put the schema
// there.
const dialTimeout = 10 * time.Second

// OpenPostgres returns the journal named queue, held in the database at url,
// creating the table it needs when it is not there yet.
//
// It reaches the database now rather than on the first write, so a mount that
// cannot record anything is a boot failure an operator can still act on instead
// of a path that fails later under load.
func OpenPostgres(url, queue string) (*Postgres, error) {
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		return nil, fmt.Errorf("queue %q: %w", queue, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("queue %q: %w", queue, err)
	}
	return &Postgres{pool: pool, queue: queue}, nil
}

// Close gives back the connections. Nothing in the port closes a plugin, so
// this is for a caller that owns the journal's lifetime and not for the tree.
func (p *Postgres) Close() { p.pool.Close() }

// Record queues a message under key, replacing the one pending there.
//
// The conflict clause is the outbox: the same statement that stores the body
// clears carried_at, so a message that changed before anyone took it is carried
// again as it now stands, and no window exists where a body is stored and the
// intent to publish it is not.
func (p *Postgres) Record(ctx context.Context, key string, body []byte, mode fs.FileMode) error {
	const record = `
INSERT INTO queue_messages (queue, key, body, mode)
VALUES ($1, $2, $3, $4)
ON CONFLICT (queue, key) WHERE claimed_at IS NULL
DO UPDATE SET body = EXCLUDED.body, mode = EXCLUDED.mode, recorded_at = now(), carried_at = NULL
`
	// A nil slice is empty content, and the driver would send it as NULL: an
	// empty message and no message are the same value in Go and different rows
	// in Postgres. Translating here is the same move as turning the driver's
	// empty result into fs.ErrNotExist.
	if body == nil {
		body = []byte{}
	}
	if _, err := p.pool.Exec(ctx, record, p.queue, key, body, int32(mode.Perm())); err != nil {
		return fmt.Errorf("record %q: %w", key, err)
	}
	return nil
}

// Claim takes the message pending under key out of the queue.
//
// The lock skips a row another reader already holds rather than waiting behind
// it, so two consumers of one queue take different messages instead of taking
// turns.
func (p *Postgres) Claim(ctx context.Context, key string) ([]byte, error) {
	const claim = `
UPDATE queue_messages SET claimed_at = now()
WHERE id = (
	SELECT id FROM queue_messages
	WHERE queue = $1 AND key = $2 AND claimed_at IS NULL
	ORDER BY id LIMIT 1
	FOR UPDATE SKIP LOCKED
)
RETURNING body
`
	var body []byte
	if err := p.pool.QueryRow(ctx, claim, p.queue, key).Scan(&body); err != nil {
		return nil, p.missing("claim", key, err)
	}
	return body, nil
}

// Peek describes the message pending under key without taking it.
func (p *Postgres) Peek(ctx context.Context, key string) (Entry, error) {
	const peek = `
SELECT key, octet_length(body), mode, recorded_at FROM queue_messages
WHERE queue = $1 AND key = $2 AND claimed_at IS NULL
`
	var entry Entry
	var mode int32
	if err := p.pool.QueryRow(ctx, peek, p.queue, key).Scan(&entry.Key, &entry.Size, &mode, &entry.Recorded); err != nil {
		return Entry{}, p.missing("peek", key, err)
	}
	entry.Mode = fs.FileMode(mode)
	return entry, nil
}

// List describes every pending message under prefix, however deep.
func (p *Postgres) List(ctx context.Context, prefix string) ([]Entry, error) {
	const list = `
SELECT key, octet_length(body), mode, recorded_at FROM queue_messages
WHERE queue = $1 AND claimed_at IS NULL AND starts_with(key, $2)
ORDER BY key
`
	rows, err := p.pool.Query(ctx, list, p.queue, prefix)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", prefix, err)
	}
	defer rows.Close()

	var found []Entry
	for rows.Next() {
		var entry Entry
		var mode int32
		if err := rows.Scan(&entry.Key, &entry.Size, &mode, &entry.Recorded); err != nil {
			return nil, fmt.Errorf("list %q: %w", prefix, err)
		}
		entry.Mode = fs.FileMode(mode)
		found = append(found, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %q: %w", prefix, err)
	}
	return found, nil
}

// Drop takes the message pending under key out of the journal without anyone
// consuming it. It is deleted rather than claimed: a purge is not a delivery,
// and a backfill should not replay what an operator threw away.
func (p *Postgres) Drop(ctx context.Context, key string) error {
	const drop = `DELETE FROM queue_messages WHERE queue = $1 AND key = $2 AND claimed_at IS NULL`

	tag, err := p.pool.Exec(ctx, drop, p.queue, key)
	if err != nil {
		return fmt.Errorf("drop %q: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("drop %q: %w", key, fs.ErrNotExist)
	}
	return nil
}

// DropAll takes every pending message under prefix out of the journal.
func (p *Postgres) DropAll(ctx context.Context, prefix string) error {
	const dropAll = `
DELETE FROM queue_messages WHERE queue = $1 AND claimed_at IS NULL AND starts_with(key, $2)
`
	if _, err := p.pool.Exec(ctx, dropAll, p.queue, prefix); err != nil {
		return fmt.Errorf("drop_all %q: %w", prefix, err)
	}
	return nil
}

// Move puts the message pending under from at to.
//
// Both statements are one transaction, because the landing has to be gone
// before the move lands on it and a move that failed in between would leave the
// queue with neither message where the caller asked for it.
func (p *Postgres) Move(ctx context.Context, from, to string) error {
	const clear = `DELETE FROM queue_messages WHERE queue = $1 AND key = $2 AND claimed_at IS NULL`
	const move = `UPDATE queue_messages SET key = $3 WHERE queue = $1 AND key = $2 AND claimed_at IS NULL`

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("move %q: %w", from, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, clear, p.queue, to); err != nil {
		return fmt.Errorf("move %q to %q: %w", from, to, err)
	}
	tag, err := tx.Exec(ctx, move, p.queue, from, to)
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

// Chmod sets the permission bits the message pending under key carries.
func (p *Postgres) Chmod(ctx context.Context, key string, mode fs.FileMode) error {
	const chmod = `
UPDATE queue_messages SET mode = $3 WHERE queue = $1 AND key = $2 AND claimed_at IS NULL
`
	tag, err := p.pool.Exec(ctx, chmod, p.queue, key, int32(mode.Perm()))
	if err != nil {
		return fmt.Errorf("chmod %q: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("chmod %q: %w", key, fs.ErrNotExist)
	}
	return nil
}

// Uncarried returns what has been recorded and not yet published, oldest first.
func (p *Postgres) Uncarried(ctx context.Context, limit int) ([]Carry, error) {
	const uncarried = `
SELECT id, key, body FROM queue_messages
WHERE queue = $1 AND carried_at IS NULL
ORDER BY id LIMIT $2
`
	return p.carries(ctx, "uncarried", uncarried, p.queue, limit)
}

// Carried records that the broker has been told.
func (p *Postgres) Carried(ctx context.Context, ids []int64) error {
	const carried = `UPDATE queue_messages SET carried_at = now() WHERE queue = $1 AND id = ANY($2)`

	if _, err := p.pool.Exec(ctx, carried, p.queue, ids); err != nil {
		return fmt.Errorf("carried: %w", err)
	}
	return nil
}

// Recorded returns the journal from after onwards, claimed or not, carried or
// not. It is the backfill, and it reads rows rather than anything the broker
// kept.
func (p *Postgres) Recorded(ctx context.Context, after int64, limit int) ([]Carry, error) {
	const recorded = `
SELECT id, key, body FROM queue_messages
WHERE queue = $1 AND id > $2
ORDER BY id LIMIT $3
`
	return p.carries(ctx, "recorded", recorded, p.queue, after, limit)
}

// carries reads the rows the relay works from, which the outbox's two queries
// shape the same way.
func (p *Postgres) carries(ctx context.Context, verb, query string, args ...any) ([]Carry, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", verb, err)
	}
	defer rows.Close()

	var found []Carry
	for rows.Next() {
		var message Carry
		if err := rows.Scan(&message.ID, &message.Key, &message.Body); err != nil {
			return nil, fmt.Errorf("%s: %w", verb, err)
		}
		found = append(found, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", verb, err)
	}
	return found, nil
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
