package queuefs

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

// prefetch is how many notifications the broker hands a listener before it acks
// one. It is one because a notification is only worth holding while the worker
// it belongs to is claiming: two workers on the same queue should share what
// arrives, and a worker that dies should have at most one delivery to give
// back.
const prefetch = 1

// Consumer is the broker seen from the waiting end: a durable queue bound to
// the exchange the relay publishes to, handing over the keys it hears about.
//
// It is a type of its own rather than two more methods on AMQP because the two
// ends hold a connection differently. Publish redials whenever the server
// closed one under it; a listener holds one open for as long as it listens, and
// a redial underneath it would leave a delivery channel nobody reads.
//
// The queue is durable, named and bound before it is listened to, so two
// workers under one name compete for the same messages, a worker that was away
// is handed what arrived while it was, and a worker starting for the first time
// hears about what was published between its queue being made and its first
// call to Listen. Deliveries are acknowledged after the worker has done the
// work, which is what makes a worker that dies between hearing about a message
// and claiming it a worker whose notification comes back.
type Consumer struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	queue      string
}

// DialConsumer returns a listener on queue, bound to pattern on the exchange at
// url and declaring what is not there yet.
//
// The pattern is in the tree's vocabulary — "index/#" hears about everything
// under the queue's index prefix — because a caller of the bus has no reason to
// know what a routing key looks like.
//
// It connects now for the reason DialAMQP does: a listener that cannot reach
// the broker is worth failing at while an operator is still watching, rather
// than looking like a queue nobody is publishing to.
func DialConsumer(url, exchange, queue, pattern string) (*Consumer, error) {
	connection, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial %q: %w", queue, err)
	}

	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("channel %q: %w", queue, err)
	}
	if err := subscribe(channel, exchange, queue, pattern); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &Consumer{connection: connection, channel: channel, queue: queue}, nil
}

// subscribe puts the exchange, the queue and the binding between them on the
// server. The exchange is declared with the same durability the publishing end
// declares it with, because whichever end reaches the broker first is the end
// that makes it.
func subscribe(channel *amqp.Channel, exchange, queue, pattern string) error {
	if err := channel.ExchangeDeclare(exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare %q: %w", exchange, err)
	}
	if _, err := channel.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare %q: %w", queue, err)
	}
	if err := channel.QueueBind(queue, routingKey(pattern), exchange, false, nil); err != nil {
		return fmt.Errorf("bind %q to %q: %w", queue, pattern, err)
	}
	if err := channel.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("qos %q: %w", queue, err)
	}
	return nil
}

// Close gives back the connection. Nothing in the port closes a listener, so
// this is for the caller that owns its lifetime.
func (c *Consumer) Close() error {
	if err := c.connection.Close(); err != nil {
		return fmt.Errorf("close %q: %w", c.queue, err)
	}
	return nil
}

// Listen calls notify with the key of every message this consumer's queue is
// handed.
//
// A delivery is acknowledged once notify has returned nil, so a listener that
// stops before that leaves the notification on the server for whoever listens
// next. The message itself was never at risk either way: it is in the journal
// until someone claims it, and the notification only says it is there.
func (c *Consumer) Listen(ctx context.Context, notify Notify) error {
	deliveries, err := c.channel.ConsumeWithContext(ctx, c.queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", c.queue, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, open := <-deliveries:
			if !open {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("listen on %q: the broker stopped delivering", c.queue)
			}
			if err := c.deliver(ctx, delivery, notify); err != nil {
				return err
			}
		}
	}
}

// deliver hands one delivery to notify and acknowledges it.
//
// The key travels as the message id rather than being read back out of the
// routing key. A routing key is the key with its separators as dots, which is a
// topic and not a name: a key whose element contains a dot makes a topic that
// cannot be turned back into it. The id is what the message was recorded under,
// exactly.
func (c *Consumer) deliver(ctx context.Context, delivery amqp.Delivery, notify Notify) error {
	if delivery.MessageId == "" {
		return fmt.Errorf("listen on %q: a delivery under %q carries no key", c.queue, delivery.RoutingKey)
	}
	if err := notify(ctx, delivery.MessageId); err != nil {
		return err
	}
	if err := delivery.Ack(false); err != nil {
		return fmt.Errorf("ack %q on %q: %w", delivery.MessageId, c.queue, err)
	}
	return nil
}

// Consuming returns a listener on the broker a mount built from settings
// publishes to, for the consumer group named group.
//
// It takes the mount's settings rather than a URL and an exchange because the
// two ends have to agree on both, and the rule that names an exchange a mount
// did not name lives here. A composition root spelling that rule again would be
// the second place that knows it.
//
// The durable queue is the exchange and the group, so every process running the
// same group competes for the same messages and a second group hears the same
// messages independently.
func Consuming(settings map[string]string, group, pattern string) (*Consumer, error) {
	if missing := missingSettings(settings); len(missing) > 0 {
		return nil, fmt.Errorf("listening on kind %q needs %s: %w", Kind, strings.Join(missing, ", "), fs.ErrInvalid)
	}

	exchange := settings[settingExchange]
	if exchange == "" {
		exchange = exchangePrefix + settings[settingName]
	}
	return DialConsumer(settings[settingBroker], exchange, exchange+"."+group, pattern)
}
