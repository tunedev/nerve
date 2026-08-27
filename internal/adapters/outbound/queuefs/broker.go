package queuefs

import "context"

// Broker is the transport a recorded message is carried over: RabbitMQ locally
// and in the cloud, and anything else that can hand bytes to a listener.
//
// One verb, because that is all the queue needs of it. Consuming is not here:
// what a reader takes, it takes from the journal, so that losing the broker
// costs a notification rather than a message.
type Broker interface {
	// Publish carries body to whoever is listening under key.
	Publish(ctx context.Context, key string, body []byte) error
}
