# nrv-mii — releasing what a mounted plugin holds

`vfs.FileSystem` has ten verbs and no lifecycle verb. sqlfs is the first plugin
whose storage is an OS resource — a `database/sql` pool that parks a goroutine —
and `Registry.Build` hands every plugin back inside three decorators, so nothing
a caller holds can close one.

`Registry` now keeps whichever plugin it built implements `io.Closer`, and
`Registry.Release` gives them all back in the reverse of the order it built
them. `cmd/nerved` calls it after `serve` returns.

## What the pattern was

Three shapes were open: an eleventh port verb, a lifecycle interface the three
wrappers forward, or a closer the registry collects. The third won on a fact
about where the value is: `Registry.Plugin` holds the plugin **the factory just
returned**, before anything wraps it, so the type assertion there interrogates
the plugin. Everywhere else in this codebase the value in hand is the
dispatcher, which is precisely why capability discovery goes through descriptors
and accessors and never through an assertion. One place is exempt, and it is the
place that already knows every concrete type.

Collection sits in `Plugin` rather than in `Build`, so a composed kind's target
— what proxyfs and exportfs build through `Plugin` — is collected too. A target
is built before the mount over it, so releasing backwards lets the composing
mount go first.

## What surprised me

**The mount table never enters into it.** The obvious reading of "drain the
mount table" is that the table grows a `Close`. But the table holds dispatchers,
and a dispatcher would have to forward release to a plugin it cannot see past —
the same defect that forced `http.ResponseController` into the standard library,
which is the reason the accessors exist. The registry built the plugins, so the
registry is what can give them back, and the table is left as a pure dispatcher.

**The proof is a goroutine check, not an assertion.** `TestSqlfsAnswersThroughAMountTable`
now mounts through the real factory and releases in `t.Cleanup`. Deleting the
one collection line in `Plugin` leaves every assertion in that test passing and
fails the binary on goleak:

```
goleak: Errors on successful test run: found unexpected goroutines:
[Goroutine 9 in state select, with database/sql.(*DB).connectionOpener on top of the stack
```

Before this change that test could not be written at all: it opened a tree
itself and handed it to a stub factory, because a tree the registry opened was
unreachable.

**Release order needed a test with two plugins in it, not one.** The first
version released forwards and passed everything, because one plugin has no
order. What discriminates is a composed kind: target built first, mount second,
released second-then-first.

## What I would do differently

Nothing structural. `Release` clears what it held, which makes a second call a
no-op — that was added because a drain path is exactly the sort of code that
ends up called twice, not because anything calls it twice today.

## What I still do not understand

Whether a boot that fails partway through `Table` should release what it already
built. Today it does not: `Table` stops at the first spec it cannot honour,
`main` prints and exits, and the process ending is what closes the mounts made
before the bad one. That is the same gap this ticket closed for the shutdown
path, kept only because the window is a few milliseconds wide and ends in
`os.Exit`. If a caller ever recovers from a failed `Table` instead of exiting,
it is a leak.

queuefs holds a Postgres pool and an AMQP connection and implements no `Close`
at all, so it holds them until the process ends. Filed rather than fixed here:
making it releasable means deciding whether its `Journal` port grows a verb,
which is a decision about that plugin's ports and not about this mechanism.
