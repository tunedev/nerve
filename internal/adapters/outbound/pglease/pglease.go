// Package pglease is the scheduler's single-consumer exclusion, kept in
// Postgres.
//
// The claim has to be settled somewhere two processes can both reach and
// neither can win twice, and the verb set the rest of the platform stores
// things through cannot do it: it has no create-if-absent and no
// compare-and-swap, so a note written on a mount records a race rather than
// deciding one. A row with a primary key does decide one — the conflict clause
// runs inside the same statement, so of two consumers asking together exactly
// one is answered yes.
//
// Expiry is on the database's clock rather than on any consumer's. A claim
// outlives the process that took it precisely when that process died, and a
// dead host's clock is not available to ask.
package pglease

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tunedev/nerve/internal/core/schedule"
)

// Lease is the claims of every scheduler pointed at one database.
type Lease struct {
	pool *pgxpool.Pool
}

// schema is what a database with no claims in it becomes. The primary key is
// the whole guarantee: one job is one row, so a second consumer asking for a
// job that is taken conflicts rather than inserts.
const schema = `
CREATE TABLE IF NOT EXISTS job_claims (
	owner     TEXT        NOT NULL,
	workspace TEXT        NOT NULL,
	job       TEXT        NOT NULL,
	holder    TEXT        NOT NULL,
	until     TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (owner, workspace, job)
);
`

// dialTimeout bounds the one call that happens before there is a caller to
// carry a deadline: reaching the database at composition to put the table
// there.
const dialTimeout = 10 * time.Second

// Open returns the lease held in the database at url, creating the table it
// needs when it is not there yet.
//
// It reaches the database now rather than on the first claim, so a scheduler
// that can exclude nobody is a boot failure an operator can act on instead of a
// second consumer discovering it by writing the same Parquet.
func Open(url string) (*Lease, error) {
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		return nil, fmt.Errorf("job claims: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("job claims: %w", err)
	}
	return &Lease{pool: pool}, nil
}

// Close gives back the connections. Nothing in the port closes a lease, so this
// is for the caller that owns its lifetime.
func (l *Lease) Close() { l.pool.Close() }

// Acquire takes job for holder until ttl from now, reporting whether it got it.
//
// The update runs only where the standing claim has run out, so a job somebody
// is on conflicts and changes no row: refused, which is an answer rather than a
// failure. A consumer that already holds the job is refused too — it is the one
// row either way, and a holder allowed to take its own claim again is a process
// whose two goroutines are both inside the run.
func (l *Lease) Acquire(ctx context.Context, job schedule.Job, holder string, ttl time.Duration) (bool, error) {
	const take = `
INSERT INTO job_claims (owner, workspace, job, holder, until)
VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5))
ON CONFLICT (owner, workspace, job) DO UPDATE
	SET holder = EXCLUDED.holder, until = EXCLUDED.until
	WHERE job_claims.until <= now()
`
	// A conflict the clause refuses writes no row, so the row count is the
	// answer: one means this consumer has the job, none means somebody else
	// still does.
	tag, err := l.pool.Exec(ctx, take,
		job.Scope.User, job.Scope.Workspace, job.Name, holder, ttl.Seconds(),
	)
	if err != nil {
		return false, fmt.Errorf("claim job %q: %w", job.Name, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release drops the claim if holder is the one standing under it.
//
// The holder is in the condition rather than assumed, because a consumer whose
// claim already expired and was taken over would otherwise release the claim of
// the one that took it over — and that is the window where two of them run.
func (l *Lease) Release(ctx context.Context, job schedule.Job, holder string) error {
	const give = `
DELETE FROM job_claims
WHERE owner = $1 AND workspace = $2 AND job = $3 AND holder = $4
`
	if _, err := l.pool.Exec(ctx, give,
		job.Scope.User, job.Scope.Workspace, job.Name, holder,
	); err != nil {
		return fmt.Errorf("release job %q: %w", job.Name, err)
	}
	return nil
}
