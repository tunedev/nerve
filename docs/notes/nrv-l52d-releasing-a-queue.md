# nrv-l52d — releasing a queue

`queuefs.Factory` opens a `pgxpool` and dials RabbitMQ, and `queuefs.FS` had no
`Close`. `Registry.Release` collects whichever plugin a factory returns
implementing `io.Closer`, so the queue was the one shipped kind that mechanism
could not reach: both were held until the process ended.

The factory now returns a `mount` — the `*FS` embedded, plus the pool and the
connection it opened — and `Close` gives them back, broker first, then journal.

## What the pattern was

Two shapes were open, and the bead named both: `Journal` and `Broker` grow a
release verb, or the factory keeps the concrete `*Postgres` and `*AMQP` and
closes those.

The ports lost on ownership. `FS` did not open its journal — it was handed one,
and a `New` caller that passes in its own closes its own. A `Close` on `Journal`
would make every implementation carry a lifetime it may not have (the suite's
memory journal has nothing to give back) and would have `FS` closing something
it does not own. The lifetime belongs to whatever opened the thing, which is the
factory, so the factory is what holds it.

`mount` embeds `*FS` rather than wrapping it by hand: every verb, both
accessors and the per-path descriptor are promoted, so what the mount table
dispatches to is unchanged and only `Close` is new. `FS` keeps speaking to its
two ports and names neither Postgres nor AMQP.

The two closers are narrow interfaces declared at the consumer — `pool`, whose
`Close` reports nothing, and `connection`, whose `Close` reports an error —
because `pgxpool.Pool.Close` returns nothing and a `[]io.Closer` would have
forced `*Postgres` to invent an error it cannot have.

## What surprised me

**The test that proves it needs neither Postgres nor RabbitMQ.** `Factory`
itself cannot run without both, so the assertion is one level under it: build
what the factory builds, ask it the question `Registry.Plugin` asks — is this an
`io.Closer` — and close it. Deleting the `holding` call fails
`TestAQueueGivesBackWhatTheFactoryOpened` on exactly that question, in the
package's first internal test file.

**Reverse order is the same argument the registry already makes.** The registry
releases mounts in the reverse of the order it built them; inside one mount the
factory opens the journal and then the broker, so `Close` takes them back the
other way round. The pool is given back whatever the broker said, because a
connection that will not close is not a reason to leave a database pool open.

## What I would do differently

Nothing. The alternative worth naming is a `Close` on `FS` guarded by a nil
field the factory sets, which is one type fewer and one lie more: every queue
would advertise a lifecycle verb and most of them would do nothing with it.

## What I still do not understand

`queuefs.Consumer` holds a connection of its own and has a `Close` nobody calls,
because a consumer is not a plugin and no factory builds one. When the work bus
is wired at composition, whatever owns that consumer owns its connection, and
`Registry.Release` is not it — the registry gives back what its factories
opened, and this is not one of them.
