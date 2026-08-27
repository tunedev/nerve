package queuefs

import "context"

// Notify is what a listener calls for each message it hears about. It is given
// the key the message was recorded under and nothing else: the body is in the
// journal, and coming back for it is what makes losing the broker cost a
// notification rather than the work.
//
// An error ends the listening and is what Listen returns.
type Notify func(ctx context.Context, key string) error

// Listener is the same broker seen from the end that waits on it, the way
// Outbox is the same journal seen by the relay. It is a second port rather than
// a second verb on Broker because the two ends are held by different things: a
// relay carries and a worker listens, and neither has any business doing the
// other's job.
//
// This is the whole of the work bus. There is no second way to distribute work
// here — it is the exchange the relay already publishes to, seen from the other
// end. A message is published by writing a path and taken by reading one, and
// nothing on this port carries a body.
type Listener interface {
	// Listen calls notify with the key of every message this listener hears
	// about, until ctx is done or notify returns an error.
	//
	// Which messages those are is settled when the listener is made and not
	// here. A subscription is a standing arrangement with the broker, and one
	// that began at the first call would hear nothing published before it.
	//
	// A context that is done is not an error: cancelling is how a listener is
	// stopped.
	Listen(ctx context.Context, notify Notify) error
}
