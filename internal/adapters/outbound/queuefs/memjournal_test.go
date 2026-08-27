package queuefs_test

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
)

// memJournal is a queue held in a slice. It is what makes the plugin's queue
// semantics assertable without a container: the suite that runs against
// Postgres runs against this one offline, and the two have to agree.
//
// Rows are never removed by claiming, only by dropping, because the journal is
// the record a later consumer is backfilled from. Claiming takes a message out
// of the queue; it does not take it out of the record.
type memJournal struct {
	mu   sync.Mutex
	next int64
	rows []*memRow
}

type memRow struct {
	id       int64
	key      string
	body     []byte
	mode     fs.FileMode
	recorded time.Time
	carried  bool
	claimed  bool
}

func newMemJournal() *memJournal { return &memJournal{} }

// pending is the row a key addresses: the one queued under it and not yet
// claimed. At most one row is ever pending under a key, because recording
// replaces rather than queueing a second.
func (j *memJournal) pending(key string) *memRow {
	for _, row := range j.rows {
		if row.key == key && !row.claimed {
			return row
		}
	}
	return nil
}

func (j *memJournal) Record(_ context.Context, key string, body []byte, mode fs.FileMode) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	stored := slices.Clone(body)
	if row := j.pending(key); row != nil {
		row.body, row.mode, row.recorded, row.carried = stored, mode, time.Now(), false
		return nil
	}
	j.next++
	j.rows = append(j.rows, &memRow{id: j.next, key: key, body: stored, mode: mode, recorded: time.Now()})
	return nil
}

func (j *memJournal) Claim(_ context.Context, key string) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	row := j.pending(key)
	if row == nil {
		return nil, fmt.Errorf("claim %q: %w", key, fs.ErrNotExist)
	}
	row.claimed = true
	return slices.Clone(row.body), nil
}

func (j *memJournal) Peek(_ context.Context, key string) (queuefs.Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	row := j.pending(key)
	if row == nil {
		return queuefs.Entry{}, fmt.Errorf("peek %q: %w", key, fs.ErrNotExist)
	}
	return row.entry(), nil
}

func (j *memJournal) List(_ context.Context, prefix string) ([]queuefs.Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	var found []queuefs.Entry
	for _, row := range j.rows {
		if !row.claimed && strings.HasPrefix(row.key, prefix) {
			found = append(found, row.entry())
		}
	}
	slices.SortFunc(found, func(a, b queuefs.Entry) int { return strings.Compare(a.Key, b.Key) })
	return found, nil
}

func (j *memJournal) Drop(_ context.Context, key string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	row := j.pending(key)
	if row == nil {
		return fmt.Errorf("drop %q: %w", key, fs.ErrNotExist)
	}
	j.rows = slices.DeleteFunc(j.rows, func(other *memRow) bool { return other == row })
	return nil
}

func (j *memJournal) DropAll(_ context.Context, prefix string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.rows = slices.DeleteFunc(j.rows, func(row *memRow) bool {
		return !row.claimed && strings.HasPrefix(row.key, prefix)
	})
	return nil
}

func (j *memJournal) Move(_ context.Context, from, to string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	row := j.pending(from)
	if row == nil {
		return fmt.Errorf("move %q: %w", from, fs.ErrNotExist)
	}
	if landing := j.pending(to); landing != nil {
		j.rows = slices.DeleteFunc(j.rows, func(other *memRow) bool { return other == landing })
	}
	row.key = to
	return nil
}

func (j *memJournal) Chmod(_ context.Context, key string, mode fs.FileMode) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	row := j.pending(key)
	if row == nil {
		return fmt.Errorf("chmod %q: %w", key, fs.ErrNotExist)
	}
	row.mode = mode
	return nil
}

func (j *memJournal) Uncarried(_ context.Context, limit int) ([]queuefs.Carry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	var waiting []queuefs.Carry
	for _, row := range j.rows {
		if !row.carried {
			waiting = append(waiting, row.carry())
		}
		if len(waiting) == limit {
			break
		}
	}
	return waiting, nil
}

func (j *memJournal) Carried(_ context.Context, ids []int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	for _, row := range j.rows {
		if slices.Contains(ids, row.id) {
			row.carried = true
		}
	}
	return nil
}

func (j *memJournal) Recorded(_ context.Context, after int64, limit int) ([]queuefs.Carry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	var history []queuefs.Carry
	for _, row := range j.rows {
		if row.id > after {
			history = append(history, row.carry())
		}
		if len(history) == limit {
			break
		}
	}
	return history, nil
}

func (r *memRow) entry() queuefs.Entry {
	return queuefs.Entry{Key: r.key, Size: int64(len(r.body)), Mode: r.mode, Recorded: r.recorded}
}

func (r *memRow) carry() queuefs.Carry {
	return queuefs.Carry{ID: r.id, Key: r.key, Body: slices.Clone(r.body)}
}

// memBroker is a broker that keeps what it was told and can be told to refuse,
// which is how a queue is asked what it does when the broker is not there.
type memBroker struct {
	mu        sync.Mutex
	refusal   error
	published []queuefs.Carry
}

func newMemBroker() *memBroker { return &memBroker{} }

func (b *memBroker) Publish(_ context.Context, key string, body []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.refusal != nil {
		return b.refusal
	}
	b.published = append(b.published, queuefs.Carry{Key: key, Body: slices.Clone(body)})
	return nil
}

// refuse makes every publish fail, which is the broker being gone.
func (b *memBroker) refuse(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refusal = err
}

// carried is what the broker was told, in the order it was told.
func (b *memBroker) carried() []queuefs.Carry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.published)
}

// keys is what the broker was told, by name, which is what most assertions
// here are about.
func (b *memBroker) keys() []string {
	published := b.carried()
	named := make([]string, len(published))
	for i, message := range published {
		named[i] = message.Key
	}
	return named
}

// memListener is the broker the cases publish through, seen from the waiting
// end: one broker, two ends, which is the point of the port.
type memListener struct {
	broker  *memBroker
	pattern string
}

var _ queuefs.Listener = newMemBroker().listen("#")

// listen is a standing subscription to what the broker is told under pattern.
func (b *memBroker) listen(pattern string) *memListener {
	return &memListener{broker: b, pattern: pattern}
}

// Listen hands over what the broker was told, in the order it was told. It ends
// when it runs out rather than waiting for more, so a case that has published
// everything it is going to publish gets a worker that finishes instead of one
// that hangs.
func (l *memListener) Listen(ctx context.Context, notify queuefs.Notify) error {
	for _, key := range l.broker.keys() {
		if !heard(l.pattern, key) {
			continue
		}
		if err := notify(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// heard is as much of a topic pattern as the cases need: "#" hears everything
// and "prefix/#" hears everything under it.
func heard(pattern, key string) bool {
	under, wildcard := strings.CutSuffix(pattern, "#")
	if !wildcard {
		return pattern == key
	}
	return strings.HasPrefix(key, under)
}
