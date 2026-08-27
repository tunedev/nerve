package queuefs

import (
	"context"
	"fmt"
)

// DefaultBatch is how many messages one pass of the relay carries. It bounds
// the work a single write pays for when the broker has been away.
const DefaultBatch = 64

// Relay carries what the journal recorded to the broker, oldest first, and
// records what it carried. It is the outbox pattern with the two tables
// collapsed into one: a message is its own outbox entry, so recording it and
// recording the intent to publish it are the same commit and cannot come apart.
//
// It holds no goroutine. A write drives it, which means a broker that was down
// is drained by the next write rather than by a pump nobody owns — and a queue
// nothing writes to has nothing to carry.
type Relay struct {
	outbox Outbox
	broker Broker
	batch  int
}

// NewRelay returns a relay carrying up to batch messages a pass. A batch that
// is zero or negative takes DefaultBatch: a relay that carries nothing a pass
// is a broker that is never told.
func NewRelay(outbox Outbox, broker Broker, batch int) *Relay {
	if batch <= 0 {
		batch = DefaultBatch
	}
	return &Relay{outbox: outbox, broker: broker, batch: batch}
}

// Carry publishes what the journal recorded and the broker has not been told
// about, and returns how many it carried.
//
// It stops at the first message the broker refuses and reports it. What was
// carried before the refusal stays carried; what was not stays in the journal,
// to be carried by the next pass. Nothing is lost either way, which is the
// whole reason the record comes first.
func (r *Relay) Carry(ctx context.Context) (int, error) {
	waiting, err := r.outbox.Uncarried(ctx, r.batch)
	if err != nil {
		return 0, fmt.Errorf("carry: %w", err)
	}

	carried := make([]int64, 0, len(waiting))
	var refused error
	for _, message := range waiting {
		if err := r.broker.Publish(ctx, message.Key, message.Body); err != nil {
			refused = fmt.Errorf("carry %q: %w", message.Key, err)
			break
		}
		carried = append(carried, message.ID)
	}

	if len(carried) > 0 {
		if err := r.outbox.Carried(ctx, carried); err != nil {
			return 0, fmt.Errorf("carry: %w", err)
		}
	}
	return len(carried), refused
}

// Backfill publishes everything the journal holds after id, claimed or not,
// carried or not, and returns the id it reached. A consumer that was not there
// when a message was first carried is caught up by calling this until the id
// stops moving.
//
// It marks nothing. Carrying is what the broker was told once; a backfill is
// telling it again on purpose, and a backfill that consumed the outbox would
// make the next one silent.
func (r *Relay) Backfill(ctx context.Context, after int64) (int64, error) {
	history, err := r.outbox.Recorded(ctx, after, r.batch)
	if err != nil {
		return after, fmt.Errorf("backfill after %d: %w", after, err)
	}

	for _, message := range history {
		if err := r.broker.Publish(ctx, message.Key, message.Body); err != nil {
			return after, fmt.Errorf("backfill %q: %w", message.Key, err)
		}
		after = message.ID
	}
	return after, nil
}
