package queuefs_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/tunedev/nerve/internal/adapters/outbound/queuefs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// journalEnv turns this file on. Nothing here runs without a database to talk
// to, which is what keeps the rest of the package's tests true on a plane: the
// semantics are asserted against a queue in a slice, and this asserts that the
// two shipped sides keep the same promises.
//
// `make up` starts what these defaults point at.
const journalEnv = "NERVE_QUEUEFS_JOURNAL"

// brokerEnv names the broker, which docker-compose.yml also starts. It has a
// default because a run against Postgres without a broker would be half the
// point: the message reaching RabbitMQ is the other half.
const brokerEnv = "NERVE_QUEUEFS_BROKER"

const defaultBroker = "amqp://nerve:nerve@127.0.0.1:5672/"

// deliveryWait is how long a case waits for a published message to come back
// round, and pollInterval is how often it asks. A broker on the same machine
// answers in milliseconds; the wait is long enough that a loaded one still
// does, and short enough that a message that will never arrive is a failure
// rather than a hung run.
const (
	deliveryWait = 5 * time.Second
	pollInterval = 20 * time.Millisecond
)

// The same suite as the offline run, against Postgres and RabbitMQ. Each case
// gets a queue of its own inside one table, which is what "a queue with nothing
// in it" has to mean when the queue outlives the process.
func TestConformanceAgainstTheStack(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: stackQueue, Root: "/" + queuefs.Jobs})
}

func TestConformanceOfTheMountShapeAgainstTheStack(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{New: stackQueue, Root: "/"})
}

// The acceptance against the real journal: the broker is not there for the
// whole of the write, and Postgres has the message anyway. The relay carries it
// once the broker is back.
func TestTheStackLosesNoMessageWhenTheBrokerIsGone(t *testing.T) {
	journal := stackJournal(t, "durability")
	broker := newMemBroker()
	fsys := queuefs.New("durability", journal, queuefs.NewRelay(journal, broker, 0))

	broker.refuse(errGone)
	mustWrite(t, fsys, "/jobs/first", "one")

	if told := broker.keys(); len(told) != 0 {
		t.Fatalf("the broker was told %v while it was gone, want nothing", told)
	}
	if content := mustRead(t, fsys, "/jobs/first"); content != "one" {
		t.Errorf("Read from Postgres of a message the broker never heard about = %q, want %q", content, "one")
	}

	broker.refuse(nil)
	mustWrite(t, fsys, "/jobs/second", "two")

	want := []string{"first", "second"}
	if told := broker.keys(); !slices.Equal(told, want) {
		t.Errorf("the broker was told %v once it came back, want %v", told, want)
	}
}

// The whole path, end to end: a write to the tree becomes a message a consumer
// bound to the exchange receives, with its key as the topic.
func TestAWriteReachesAConsumerOnTheBroker(t *testing.T) {
	journal := stackJournal(t, "delivery")
	exchange := "nerve.test.delivery"

	broker, err := queuefs.DialAMQP(brokerURL(t), exchange)
	if err != nil {
		t.Fatalf("DialAMQP = %v", err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	})

	delivered := bindConsumer(t, exchange)
	fsys := queuefs.New("delivery", journal, queuefs.NewRelay(journal, broker, 0))
	mustWrite(t, fsys, "/jobs/index/document/42", "work")

	waiting, cancel := context.WithTimeout(t.Context(), deliveryWait)
	defer cancel()

	message, ok, err := delivered.Get(waiting)
	if err != nil || !ok {
		t.Fatalf("the consumer received %v, %v, want the message", ok, err)
	}
	if string(message.Body) != "work" {
		t.Errorf("the consumer received %q, want %q", message.Body, "work")
	}
	if message.RoutingKey != "index.document.42" {
		t.Errorf("the message arrived under %q, want %q", message.RoutingKey, "index.document.42")
	}
}

// stackQueue is the plugin over Postgres and a broker that keeps what it is
// told. The broker is in memory on purpose: the conformance suite says nothing
// about publishing, and a case that declared an exchange per run would leave
// one behind on the server for every case name the suite has.
func stackQueue(t *testing.T) vfs.FileSystem {
	t.Helper()

	name := queueName(t.Name())
	journal := stackJournal(t, name)
	return queuefs.New(name, journal, queuefs.NewRelay(journal, newMemBroker(), 0))
}

// stackJournal is a queue of its own in the shared table, emptied of everything
// it recorded when the case ends — claimed rows included, because a rerun of
// the same case would otherwise be backfilled from the last one.
func stackJournal(t *testing.T, name string) *queuefs.Postgres {
	t.Helper()

	url := os.Getenv(journalEnv)
	if url == "" {
		t.Skipf("set %s to run against Postgres; `make up` starts one", journalEnv)
	}

	journal, err := queuefs.OpenPostgres(url, name)
	if err != nil {
		t.Fatalf("OpenPostgres = %v", err)
	}
	t.Cleanup(func() {
		purge(t, url, name)
		journal.Close()
	})
	return journal
}

func brokerURL(t *testing.T) string {
	t.Helper()

	if os.Getenv(journalEnv) == "" {
		t.Skipf("set %s to run against the stack; `make up` starts it", journalEnv)
	}
	if url := os.Getenv(brokerEnv); url != "" {
		return url
	}
	return defaultBroker
}

// purge takes every row this case recorded back out of the shared table.
func purge(t *testing.T, url, name string) {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Errorf("purge %q: %v", name, err)
		return
	}
	defer pool.Close()

	if _, err := pool.Exec(context.Background(), `DELETE FROM queue_messages WHERE queue = $1`, name); err != nil {
		t.Errorf("purge %q: %v", name, err)
	}
}

// queueName turns a case name into something a queue can be called, so two
// cases never share one.
func queueName(name string) string {
	return "test." + strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' {
			return '.'
		}
		return r
	}, name)
}

// consumer is a queue bound to the exchange, which is what a message published
// by the relay has to reach for the broker leg to mean anything.
type consumer struct {
	channel *amqp.Channel
	queue   string
}

// bindConsumer declares a queue that is gone when the case ends and binds it to
// every topic on the exchange.
func bindConsumer(t *testing.T, exchange string) consumer {
	t.Helper()

	connection, err := amqp.Dial(brokerURL(t))
	if err != nil {
		t.Fatalf("dial the broker = %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close the consumer = %v", err)
		}
	})

	channel, err := connection.Channel()
	if err != nil {
		t.Fatalf("channel = %v", err)
	}
	if err := channel.ExchangeDeclare(exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		t.Fatalf("declare the exchange = %v", err)
	}

	queue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("declare a queue = %v", err)
	}
	if err := channel.QueueBind(queue.Name, "#", exchange, false, nil); err != nil {
		t.Fatalf("bind = %v", err)
	}
	return consumer{channel: channel, queue: queue.Name}
}

// Get takes the one message the case is waiting for, polling until the context
// runs out rather than blocking on a delivery channel the case would then have
// to drain.
func (c consumer) Get(ctx context.Context) (amqp.Delivery, bool, error) {
	for {
		message, ok, err := c.channel.Get(c.queue, true)
		if err != nil || ok {
			return message, ok, err
		}
		select {
		case <-ctx.Done():
			return amqp.Delivery{}, false, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
