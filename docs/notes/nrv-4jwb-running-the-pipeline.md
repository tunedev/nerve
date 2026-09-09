# nrv-4jwb — Running the ingest pipeline

Every piece of ingestion was delivered and tested and nothing ran it. The
composition root now names a queue, writes the resources tree through an
observer that publishes to it, and owns a goroutine that takes the jobs back off
it with the pipeline behind them.

Four things and one decision:

```
config ingest-queue   -> the queue mount jobs are published under
config ingest-root    -> where the indexing ledger stands
uploads()             -> the resources tree wrapped in vfs.WithWriteObserver
                         over a queuefs.Emitter
consumer()            -> queuefs.Consuming listener, queuefs.Worker,
                         ingest.Pipeline, on a goroutine main cancels
```

## What the pattern was

Nothing new was built. The whole increment is ordering and ownership: which tree
the resource store writes through, where the listener's broker settings come
from, and who cancels the goroutine.

**The listener reads the queue mount's own settings.** `queuefs.Consuming` takes
the mount's `map[string]string` rather than a URL and an exchange, because the
publishing end and the waiting end have to agree on both, and the rule that names
an exchange a mount did not name lives in one place. A second pair of settings in
config would be the same broker spelled twice, and the day they disagreed the
process would look healthy and hear nothing. The durable queue is the exchange
and a group name, so two nerved processes compete for the same jobs and a second
kind of consumer would hear them independently.

**The observer goes over the mount table, not under it.** `uploads` wraps the
table the resource store is handed, and the emitter publishes through the plain
table. What is observed is exactly the writes a resource makes; the queue's own
writes are not reported, and there is no loop to reason about.

**The pipeline's own writes queue jobs, and that is fine.** The text sidecar is
written back into the observed tree, so it queues a job of its own. The worker
takes it and does nothing: only a resource's meta file declares a resource, and
`Store.Resolve` says so. It costs one no-op job per document and no branch
anywhere.

## The decision the ticket asked for

A jobs mount that cannot hold a job key loses the work silently. A job key is a
whole changed path, a tree mount refuses a deep key whose parents do not exist,
and `observed.report` drops what the observer answers by design — so the process
would queue nothing, take nothing, say nothing and look healthy.

That is now a boot failure. The queue prefix is checked for the two descriptors a
queue has, and the message says what the mount is not:

```
$ NERVE_CONFIG=... ./bin/nerved      # with a memfs mounted at /queue
nerved: ingest-queue "/queue": /queue/jobs is not a flat key space, and a job key
is a whole path: the jobs would be refused, the report dropped and nothing said:
invalid argument
$ echo $?
1
```

Config refuses the other two ways to boot half a pipeline, for the same reason —
a worker that resolves every job to nothing, or one that answers nobody:

```
nerved: ingest-queue names a queue but resources-root names no tree to ingest from
nerved: ingest-queue names a queue but ingest-root names no root to record indexing under
```

## Evidence

The round trip, against the running binary with Postgres and RabbitMQ up. A
document is put into the resources tree, a job for it is put on the shared bus,
and the running process does the rest:

```
$ ./bin/nerved &                     # NERVE_CONFIG names /resources, /ingest, /queue
{"level":"INFO","msg":"pipeline","queue":"/queue","group":"ingest","ledger-root":"/ingest"}

$ find resources ingest -type f
resources/ada/hunt/transcript.txt/content
resources/ada/hunt/transcript.txt/meta.json
resources/ada/hunt/transcript.txt/text      <- the ingestion left this
ingest/ada/hunt/transcript.txt              <- and this

$ cat ingest/ada/hunt/transcript.txt
{"status":"indexed","at":"2026-09-09T18:32:36.597505247+01:00"}

$ psql -c "SELECT key, claimed_at IS NOT NULL FROM queue_messages WHERE queue='demo_ingest'"
 resources/ada/hunt/transcript.txt/meta.json | t
 resources/ada/hunt/transcript.txt/text      | t
```

The second row is the observer proving itself: the running process queued a job
for its own sidecar write, took it, and correctly did nothing with it.

The whole path including the upload, as a build composes it, is
`TestAFinalisedDocumentIsIngestedAndFoundByMeaning` under `make test-queue`. Two
mutations, each caught:

```
$ go test ./cmd/nerved/   # with uploads() returning the table unobserved
--- FAIL: TestAFinalisedDocumentIsIngestedAndFoundByMeaning (60.02s)
    nothing ingested "transcript.txt" in 1m0s

$ go test ./cmd/nerved/   # with the worker goroutine waiting on cancel instead of running
--- FAIL: TestAFinalisedDocumentIsIngestedAndFoundByMeaning (60.04s)
    nothing ingested "transcript.txt" in 1m0s

$ go test ./cmd/nerved/   # with the flat-key-space check removed
--- FAIL: TestAQueueThatIsNotAFlatKeySpaceIsRefusedAtBoot (0.00s)
    consumer(/queue over memfs) = ingest-queue "/queue" names no mount: file does
    not exist, want fs.ErrInvalid
```

## What surprised me

**The wiring works and the bus underneath it drops a job now and then.** The
round trip fails one run in five to ten — `make test-queue` passed 8 of 10 — and
it is not the wiring: the broker
acknowledges the publish, the binding matches, the queue is empty and the worker
is alive and hears nothing. `Relay.Carry` publishes and only then records
`carried_at`, so the notification reaches the worker while that update still
holds the row; `Postgres.Claim` skips locked rows, finds nothing, and
`Worker.take` reads that as a notification outliving its message — which is its
documented, correct behaviour for the case it cannot tell this apart from. The
job is gone for good: the row is marked carried and only a write drives a carry.

Proven against the running database rather than argued:

```
session 1: BEGIN; UPDATE queue_messages SET carried_at = now() WHERE queue='lockprobe';
           SELECT pg_sleep(6); COMMIT;
session 2: UPDATE queue_messages SET claimed_at = now()
           WHERE id = (SELECT id ... FOR UPDATE SKIP LOCKED) RETURNING 'CLAIMED';
           -> UPDATE 0
```

Filed as nrv-93kh with the candidate fixes; it is a decision inside queuefs and
not one to make from the composition root. The test stays in `make test-queue`
and names the bead in its failure message. A gate that went green by dropping it
would be reporting that uploads are ingested, which is not true one time in
several; the red run is the suite telling the truth about the platform, and it
stops being red when nrv-93kh is fixed.

**Publisher confirms were the first suspect and were wrong.** Every publish came
back acked with no channel close, which is what ruled out the client-to-broker
half and left only the claim.

## What I would do differently

The first version of the wait loop returned at the first status the ledger was
not pending on, which is `working` — the pipeline starting rather than the
pipeline finishing. It failed pointing at a stage that had not run yet.

## What I still do not understand

**Nothing puts a document into a running nerved from outside it.** The admin
surface is read only by design and fusefs is wired to nothing, so the only
inbound write is a caller inside the process. The round trip above stands a job
injection in for that upload, and until an inbound surface exists there is no
honest way to do better.

**Nothing indexes.** `ingest.Indexer` has no implementation, so the stage list is
extraction alone and "retrievable by meaning" is term matching over the words the
extraction left rather than a vector index. Which mount an indexer would write
into is a config surface that does not exist yet.

**Whether the group name should be config.** It is a constant, so two kinds of
consumer on one queue is a code change rather than a line. Nothing needs the
second kind yet, and inventing the setting now would be a knob with one value.
