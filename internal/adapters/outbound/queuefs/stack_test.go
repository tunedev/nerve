package queuefs_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
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

// The work bus over the shipped pair. A write to the tree becomes a
// notification on RabbitMQ, and the worker it reaches comes back to Postgres
// for the body — which is why losing the broker costs a notification rather
// than the work.
func TestAWrittenMessageReachesAWorkerOverTheBroker(t *testing.T) {
	journal := stackJournal(t, "bus")
	exchange, queue := "nerve.test.bus", "nerve.test.bus.worker"

	listener := workerQueue(t, exchange, queue, "index/"+everything)
	deleteQueue(t, queue)
	defer closeListener(t, listener)

	tree := busTree(t, "bus", journal, exchange)
	mustWrite(t, tree, busMount+"/jobs/index/document/42", "work")

	handled := make(chan string, 1)
	worker := queuefs.NewWorker(tree, busMount, listener, reporter(handled))

	if did := runUntil(t, worker, handled); did != "index/document/42=work" {
		t.Errorf("the worker did %q, want %q", did, "index/document/42=work")
	}
	if left := queued(t, journal); len(left) != 0 {
		t.Errorf("the queue still holds %v, want nothing", left)
	}
}

// The crash simulation over the shipped broker. A worker is told there is work
// and its process ends before it claims anything, so the delivery is never
// acknowledged: RabbitMQ hands it to the worker that comes next on the same
// queue, and that one claims the body from Postgres and does the work.
//
// This is the half the offline suite can only model. The journal keeps the
// message whatever becomes of the broker; this is the broker keeping the
// notification, which is what makes the gap between hearing and claiming
// survivable rather than merely small.
func TestACrashBeforeClaimingLeavesTheWorkOnTheBroker(t *testing.T) {
	journal := stackJournal(t, "crash")
	exchange, queue := "nerve.test.crash", "nerve.test.crash.worker"

	dying := workerQueue(t, exchange, queue, everything)
	deleteQueue(t, queue)

	tree := busTree(t, "crash", journal, exchange)
	mustWrite(t, tree, busMount+"/jobs/index/42", "work")

	var never record
	crashed := queuefs.NewWorker(tree, busMount, crashAfter(dying, 0), never.handle)

	listening, stop := context.WithTimeout(t.Context(), deliveryWait)
	defer stop()

	if err := crashed.Run(listening); !errors.Is(err, errCrashed) {
		t.Fatalf("Run of the worker that dies = %v, want %v", err, errCrashed)
	}
	if did := never.handled(); len(did) != 0 {
		t.Fatalf("the worker that died did %v, want nothing", did)
	}
	if err := dying.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}

	next := workerQueue(t, exchange, queue, everything)
	defer closeListener(t, next)

	handled := make(chan string, 1)
	worker := queuefs.NewWorker(tree, busMount, next, reporter(handled))

	if did := runUntil(t, worker, handled); did != "index/42=work" {
		t.Errorf("the worker that came next did %q, want %q", did, "index/42=work")
	}
	if left := queued(t, journal); len(left) != 0 {
		t.Errorf("the queue still holds %v, want nothing", left)
	}
}

// The awaiting end over the shipped broker. Two callers on queues of their own
// wait on the same job; a worker does it and reports; RabbitMQ hands the
// completion to both and neither polls anything.
//
// This is the half the offline suite can only model. Waiting is broadcast
// because the broker fans a completion out to every queue bound to it — and
// two callers sharing one queue name here would compete, so one of them would
// wait out its deadline for an answer the other was handed.
func TestTwoCallersAwaitOneJobOverTheBroker(t *testing.T) {
	journal := stackJournal(t, "await")
	exchange, queue := "nerve.test.await", "nerve.test.await.worker"

	listener := workerQueue(t, exchange, queue, "index/"+everything)
	deleteQueue(t, queue)
	defer closeListener(t, listener)

	first := waiterQueue(t, exchange, "nerve.test.await.first")
	defer closeListener(t, first)
	second := waiterQueue(t, exchange, "nerve.test.await.second")
	defer closeListener(t, second)

	mine, yours := queuefs.NewWaiter(first), queuefs.NewWaiter(second)
	ours := []*queuefs.Future{mine.Expect("index/42"), yours.Expect("index/42")}

	tree := busTree(t, "await", journal, exchange)
	mustWrite(t, tree, busMount+"/jobs/index/42", "work")

	waiting, stop := context.WithTimeout(t.Context(), deliveryWait)
	defer stop()

	heard := make(chan error, len(ours))
	for _, waiter := range []*queuefs.Waiter{mine, yours} {
		go func() { heard <- waiter.Run(waiting) }()
	}

	handled := make(chan string, 1)
	worker := queuefs.NewWorker(tree, busMount, listener,
		queuefs.Reporting(reporter(handled), stackBroker(t, exchange)))
	if did := runUntil(t, worker, handled); did != "index/42=work" {
		t.Errorf("the worker did %q, want %q", did, "index/42=work")
	}

	for i, future := range ours {
		if err := future.Wait(waiting); err != nil {
			t.Errorf("Wait by caller %d = %v, want nil", i+1, err)
		}
	}

	stop()
	for range ours {
		if err := <-heard; err != nil {
			t.Errorf("Run of a waiter = %v, want nil", err)
		}
	}
}

// waiterQueue is a listener on a durable queue of its own, bound to every
// completion the bus carries.
func waiterQueue(t *testing.T, exchange, queue string) *queuefs.Consumer {
	t.Helper()

	listener := workerQueue(t, exchange, queue, queuefs.Completions)
	deleteQueue(t, queue)
	return listener
}

// busTree is a queue over the real journal and the real broker, mounted where a
// worker will look for it.
func busTree(t *testing.T, name string, journal *queuefs.Postgres, exchange string) *vfs.MountTable {
	t.Helper()

	broker := stackBroker(t, exchange)
	tree := vfs.NewMountTable()
	if err := tree.Mount(busMount, queuefs.New(name, journal, queuefs.NewRelay(journal, broker, 0))); err != nil {
		t.Fatalf("Mount = %v", err)
	}
	return tree
}

// stackBroker is a connection to the real exchange, given up when the case
// ends. A process holds one; a case that needs two holds two, which is what a
// worker reporting to the same exchange its work came from looks like.
func stackBroker(t *testing.T, exchange string) *queuefs.AMQP {
	t.Helper()

	broker, err := queuefs.DialAMQP(brokerURL(t), exchange)
	if err != nil {
		t.Fatalf("DialAMQP = %v", err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	return broker
}

// workerQueue is a listener on a durable queue bound to pattern. The case
// closes it, because a case where closing is the crash has to say when.
func workerQueue(t *testing.T, exchange, queue, pattern string) *queuefs.Consumer {
	t.Helper()

	listener, err := queuefs.DialConsumer(brokerURL(t), exchange, queue, pattern)
	if err != nil {
		t.Fatalf("DialConsumer = %v", err)
	}
	return listener
}

func closeListener(t *testing.T, listener *queuefs.Consumer) {
	t.Helper()

	if err := listener.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

// deleteQueue takes a worker's queue back off the server when the case ends. A
// durable queue outlives the connection that made it, which is what a worker
// wants and what would otherwise leave one behind for every case here.
func deleteQueue(t *testing.T, queue string) {
	t.Helper()

	t.Cleanup(func() {
		connection, err := amqp.Dial(brokerURL(t))
		if err != nil {
			t.Errorf("delete %q: %v", queue, err)
			return
		}
		defer connection.Close()

		channel, err := connection.Channel()
		if err != nil {
			t.Errorf("delete %q: %v", queue, err)
			return
		}
		if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
			t.Errorf("delete %q: %v", queue, err)
		}
	})
}

// reporter is a handler that hands what it was given to the case.
func reporter(handled chan<- string) queuefs.Handler {
	return func(_ context.Context, key string, body []byte) error {
		handled <- key + "=" + string(body)
		return nil
	}
}

// runUntil runs the worker until it does something or the case's wait runs out.
// A running worker is a goroutine, and cancelling its context is the defined
// way to stop one — so nothing here outlives the case.
func runUntil(t *testing.T, worker *queuefs.Worker, handled <-chan string) string {
	t.Helper()

	listening, stop := context.WithTimeout(t.Context(), deliveryWait)
	defer stop()

	ran := make(chan error, 1)
	go func() { ran <- worker.Run(listening) }()

	var did string
	select {
	case did = <-handled:
	case <-listening.Done():
		t.Errorf("the worker was told nothing in %s", deliveryWait)
	}

	stop()
	if err := <-ran; err != nil {
		t.Errorf("Run = %v", err)
	}
	return did
}

// The write observer over the shipped pair. A document is written to one mount
// and the work of indexing it is a row in Postgres, a topic on RabbitMQ and a
// key at a worker that asked for that subtree — with nobody having asked for
// the job. The worker is told the path and nothing else, which is why the body
// it receives is empty.
func TestAWriteToTheTreeIsWorkOverTheBroker(t *testing.T) {
	journal := stackJournal(t, "observed")
	exchange, queue := "nerve.test.observed", "nerve.test.observed.worker"

	listener := workerQueue(t, exchange, queue, "docs."+everything)
	deleteQueue(t, queue)
	defer closeListener(t, listener)

	tree := busTree(t, "observed", journal, exchange)
	if err := tree.Mount(docs, memfs.New()); err != nil {
		t.Fatalf("Mount = %v", err)
	}

	observed := vfs.WithWriteObserver(tree, queuefs.NewEmitter(tree, busMount))
	mustWrite(t, observed, docs+"/report.txt", "the content")

	handled := make(chan string, 1)
	worker := queuefs.NewWorker(tree, busMount, listener, reporter(handled))

	if did := runUntil(t, worker, handled); did != "docs/report.txt=" {
		t.Errorf("the worker was told %q, want %q", did, "docs/report.txt=")
	}
	if content := mustRead(t, tree, docs+"/report.txt"); content != "the content" {
		t.Errorf("the document reads back as %q, want %q", content, "the content")
	}
	if left := queued(t, journal); len(left) != 0 {
		t.Errorf("the queue still holds %v, want nothing", left)
	}
}
