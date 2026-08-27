package queuefs

import (
	"context"
	"fmt"
	"strings"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// AMQP is the broker the platform ships: RabbitMQ on the laptop and in the
// cloud, reached over one connection for the life of the process.
//
// Messages go to a durable topic exchange named for the mount, with the
// message's key as the routing key and its separators as dots — so a consumer
// binds to "index.#" and hears about everything queued under /jobs/index,
// without the queue having to know a consumer exists. The key itself travels as
// the message id, because a topic is a route and not a name: a listener that
// read the key back out of the routing key would be guessing at any element
// with a dot in it.
//
// Publishing is persistent, which is as much as a broker can promise. What it
// cannot promise is why the journal comes first: this is transport, and a
// message that never reaches it is still a message.
type AMQP struct {
	mu         sync.Mutex
	connection *amqp.Connection
	channel    *amqp.Channel
	url        string
	exchange   string
}

// separatorDot is what a key's separator becomes in a routing key. AMQP splits
// a topic on dots, so a key that is a path becomes a topic that is the same
// shape.
const separatorDot = "."

// DialAMQP returns a broker publishing to exchange on the server at url,
// declaring the exchange when it is not there yet.
//
// It connects now for the reason the journal does: a mount whose broker is
// misconfigured is worth failing at boot, where an operator is still watching.
// Losing the connection later is not, and is what Publish redials for.
func DialAMQP(url, exchange string) (*AMQP, error) {
	broker := &AMQP{url: url, exchange: exchange}
	if err := broker.connect(); err != nil {
		return nil, err
	}
	return broker, nil
}

// Close gives back the connection. Nothing in the port closes a plugin, so this
// is for a caller that owns the broker's lifetime and not for the tree.
func (b *AMQP) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.connection == nil {
		return nil
	}
	err := b.connection.Close()
	b.connection, b.channel = nil, nil
	if err != nil {
		return fmt.Errorf("close %q: %w", b.exchange, err)
	}
	return nil
}

// Publish carries body to whoever is listening under key.
//
// A connection the server closed is dialled again once and the publish retried,
// because a broker that restarted is the ordinary case and it is not a failure
// the caller can do anything with. Anything else is reported, and the relay
// leaves the message in the journal for the next pass.
func (b *AMQP) Publish(ctx context.Context, key string, body []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	err := b.publish(ctx, key, body)
	if err == nil {
		return nil
	}
	if b.connection != nil && !b.connection.IsClosed() {
		return err
	}

	if err := b.connect(); err != nil {
		return err
	}
	return b.publish(ctx, key, body)
}

// publish is one attempt on the channel already open.
func (b *AMQP) publish(ctx context.Context, key string, body []byte) error {
	if b.channel == nil {
		return fmt.Errorf("publish %q: no channel to %q", key, b.exchange)
	}

	message := amqp.Publishing{
		ContentType:  "application/octet-stream",
		DeliveryMode: amqp.Persistent,
		MessageId:    key,
		Body:         body,
	}
	if err := b.channel.PublishWithContext(ctx, b.exchange, routingKey(key), false, false, message); err != nil {
		return fmt.Errorf("publish %q to %q: %w", key, b.exchange, err)
	}
	return nil
}

// connect dials and declares. The caller holds the lock.
func (b *AMQP) connect() error {
	connection, err := amqp.Dial(b.url)
	if err != nil {
		return fmt.Errorf("dial %q: %w", b.exchange, err)
	}

	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return fmt.Errorf("channel %q: %w", b.exchange, err)
	}
	if err := channel.ExchangeDeclare(b.exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		_ = connection.Close()
		return fmt.Errorf("declare %q: %w", b.exchange, err)
	}

	b.connection, b.channel = connection, channel
	return nil
}

// routingKey is a message key as a topic: the same parts, split the way AMQP
// splits them.
func routingKey(key string) string {
	return strings.ReplaceAll(key, separator, separatorDot)
}
