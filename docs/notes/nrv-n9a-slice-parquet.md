# nrv-n9a — a slice is a Parquet file

`internal/core/projection` now reads the records a manifest names off the tree,
cuts them into slices by the column the manifest slices by, and writes one file
per slice. `internal/adapters/outbound/duckparquet` is what makes those files
Parquet: it hands the table to DuckDB and reads back the bytes.

Against DuckDB v1.5.5 named by `NERVE_DUCKDB_BIN`:

```
$ go test -count=1 -v ./internal/adapters/outbound/duckparquet/ | grep -c -- '--- PASS'
13

$ go test -count=1 -v ./internal/core/projection/ | grep -c -- '--- PASS'
76

$ go test -race -count=1 ./internal/adapters/outbound/duckparquet/
ok      github.com/tunedev/nerve/internal/adapters/outbound/duckparquet 1.124s

$ make check
...
reproducible: 6599b4f3c468df85db6c7ed339a64d8d409af1b1c560b52cbf8b10111d46b39e
```

With no DuckDB anywhere, the thirteen become ten skips and three passes, and
`make check` is green — which is the plane test, and why the engine tests are
not in it.

## What the pattern was

**The engine is a process, not a library, and the build decided that.** The
Makefile builds with `CGO_ENABLED=0` and `make repro` asserts two clean
checkouts produce identical binaries. `github.com/marcboeker/go-duckdb` needs
cgo, so wiring it into the composition root ends both, and it carries a prebuilt
engine per platform besides. Postgres, RabbitMQ and the object store are already
processes this repo talks to. An engine that is one too is the shape the repo
has: invoked rather than dialled, which is the only difference.

**The port returns bytes, not a path.** `Encoder` is given a schema and rows and
returns a file. It could have been given somewhere to write, and DuckDB would
have written there directly — but a slice lands wherever its mount is, and a
mount may be a keyspace or an object store with no path an engine can open.
Bytes keep the tree the system of record, which is the same rule the durability
tenet states for the broker.

**The encoder names its own extension.** `Extension()` is on the port, so
nothing in the core knows a slice is Parquet. A projection materialised by
something else is a different file name and no edit above the adapter.

**The core bounds the encode; the adapter does not.** The model gateway already
does this — a provider is given a deadline by the thing calling it, because how
long this build waits is the caller's policy and not the provider's. The
materialiser holds the timeout and wraps every `Encode` in it, so an engine that
hangs is a deadline rather than a materialise that never returns.

## What surprised me

**DuckDB writes byte-identical Parquet for identical input.** Four encodes of
one table produce the same bytes. E10.4 asks a re-run to be a no-op producing
the same result, and it now starts from an engine that is repeatable rather than
from a writer stamping a time or an id into every file. It is asserted here,
because if a later DuckDB stops being repeatable the convergence proof would
fail on the engine and read as if the projection had changed.

**`-safe` is not a sandbox you can use.** DuckDB's safe mode disables filesystem
operations entirely, so the encoder cannot read the rows it just wrote.
`allowed_directories` is the setting that would confine it, and it did not
refuse a read of `/etc/hostname` when set after startup — it is startup config
the CLI has no flag for. The inputs here are files this package wrote and the
statement is not user-authored, so nothing is exposed today; confining the
engine belongs with E11 rather than being half-done here.

**A record's identity being in the path decides the null rule.** A column
reading an absent field is null, not a refusal, because a record saying less
than its neighbours is still a record and a projection is asked for all of them.
The slice column is the exception: its value is the slice's file name, so a
record with nothing in it is refused rather than gathered into a slice under a
name the projection never read.

## What I would do differently

The slice key is the value spelled one way — a string as itself, an int in
decimal, a timestamp as RFC 3339 in UTC. That answers the question E10.1 left
open about float columns, and it answers it by making the file name carry a
colon. Nothing on the tree minds, and neither does Linux, but a `localfs` mount
on a filesystem that does would refuse a timestamp-sliced projection for a
reason nothing in the projection explains.

## What I still do not understand

Where a manifest lives is still open, so nothing constructs one and nothing
wires the encoder. That is the same unknown E10.1 ended on, and it is now the
only thing between this and a projection an operator can ask for. Whether the
DuckDB binary is process config beside the model endpoints, or a mount setting
belonging to the projections root, follows from the same answer.

## Deliberately not done

**No stale slice is removed.** A re-run over records whose slice column changed
leaves the file the old key wrote. That is E10.4's ticket — converging means
knowing what the previous run wrote — and doing half of it here would be a rule
about convergence enforced one ticket before the proof that reads it.

**No rollup.** E10.3 combines the slices; `RollupSchema` already derives, and
nothing computes it.

**No composition-root wiring, and no config key.** `NERVE_DUCKDB_BIN` is in
`.env.example` for `make test-projection` and is read by the tests, not by the
binary. A setting the binary resolved and printed in its effective config while
nothing could use it would be worse than none.
