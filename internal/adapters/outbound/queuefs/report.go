package queuefs

import (
	"context"
	"errors"
	"fmt"
	"path"
)

// Done is the prefix a completion is published under, and ok and failed are
// what the element after it says about the job.
//
// A completion is addressed rather than carried: everything a waiter needs is
// in the key. That is what lets every waiter hear every completion without any
// of them claiming one — a job is done once, but the fact that it is done is
// for whoever was waiting on it.
const (
	Done   = "done"
	ok     = "ok"
	failed = "failed"
)

// Completions is the pattern a waiter's listener binds to: every completion the
// bus carries, in the tree's vocabulary the way DialConsumer takes it.
const Completions = Done + separator + "#"

// Reporting returns a handler that says a job finished once handle has done it.
//
// The completion goes to the broker the relay already publishes to, which is
// why this is one bus and not two. It is the only thing on it that is not
// recorded first: a job is durable state and was journaled before anyone was
// told it existed, while a completion is a fact about a job that is no longer
// queued and that nothing will ever read back. Recording one would put a row in
// Postgres that no claim removes.
//
// So losing the broker costs a waiter its notification and leaves it to its
// deadline, and costs no work. That is the same bargain the bus already makes
// one level down, where losing the broker costs a worker its notification and
// leaves the message in the journal.
//
// A handler that fails reports the failure before returning it, so a caller
// waiting on that job hears that it will not be done rather than waiting out a
// deadline that cannot tell failed from slow. The run still ends, which is what
// a claim that cannot be given back leaves the worker no choice about.
func Reporting(handle Handler, broker Broker) Handler {
	return func(ctx context.Context, key string, body []byte) error {
		failure := handle(ctx, key, body)
		return errors.Join(failure, report(ctx, broker, key, failure))
	}
}

// report publishes what became of key. The failure travels as the body, where
// an operator watching the exchange can read it: the listening port hands over
// keys, so it is not what settles the waiter.
func report(ctx context.Context, broker Broker, key string, failure error) error {
	outcome, body := ok, []byte(nil)
	if failure != nil {
		outcome, body = failed, []byte(failure.Error())
	}
	if err := broker.Publish(ctx, path.Join(Done, outcome, key), body); err != nil {
		return fmt.Errorf("report %q: %w", key, err)
	}
	return nil
}
