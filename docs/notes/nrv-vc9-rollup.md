# nrv-vc9 — the slices combine into one file

`internal/core/projection` now writes, beside the directory holding a
projection's slices, the one table those slices roll up into: grouped by the
columns the manifest groups by, reduced by the measures it declares. `Materialise`
returns a `Result` naming both — the slice files in the order they were cut, and
the combined one.

Against DuckDB v1.5.5 named by `NERVE_DUCKDB_BIN`:

```
$ go test -count=1 -v ./internal/adapters/outbound/duckparquet/ | grep -c -- '--- PASS'
13

$ go test -count=1 -v ./internal/core/projection/ | grep -c -- '--- PASS'
84

$ go test -race -count=1 ./internal/adapters/outbound/duckparquet/
ok      github.com/tunedev/nerve/internal/adapters/outbound/duckparquet 1.186s

$ make check
...
reproducible: a7f8406357c90ec9088ef7b67c063c41b8e232167b8a9312e16e64ef435a3cba
```

With no DuckDB anywhere, ten of the thirteen skip and `make check` is green.

The end-to-end case reads the combined Parquet back through the engine:

```
applied|2|90000|2026-01-04 07:00:00+00
offer|1|120000|2026-01-03 09:00:00+00
```

Three applications at two stages, counted, their salaries added up with the one
nobody stated passed over, and the latest application at each stage.

## What the pattern was

**The rollup is computed in the core, not handed to the engine.** `Encoder` is
still one method taking a schema and rows, and it is now given two kinds of
table rather than one. Aggregating through DuckDB would have meant a second port
— an engine that reads slices back — and with it a Parquet decoder the core
would have to trust for the numbers. `Rollup` already lives in the core: it is
validated there, and `RollupSchema` already derived from it. Computing it there
too keeps one port and puts the arithmetic where the vocabulary is.

**It reads no slice back.** The rows are the ones that were just read to cut the
slices, so the combined file and the slice files come from one pass over the
tree. A rollup that re-read its own output would be a second answer to the
question of what the projection holds, and two answers is one too many for
E10.4.

**The rollup lands beside the slice directory, not in it.** Slices are named by
their key, so a projection whose slice column takes its own name — `applications`
at stage `applications` — would have a rollup and a slice competing for one
file. `/projections/applications.parquet` beside `/projections/applications/`
cannot collide with any key.

**A group's order is total or it is not an order.** Groups are sorted by the
values they group on, and where a comparison calls two of them equal, by the key
that spelled them. Without the fallback, the order of two such groups would come
from ranging a map, and E10.4 asks a re-run to produce the same table.

## What surprised me

**A missing value and an empty string have to be told apart at the key, not at
the comparison.** Grouping needs a map, a map needs a string, and the obvious
spelling makes `null` and `""` the same group. The key marks an empty cell
instead of spelling it, and length-prefixes the rest, because `"ab"` beside
`"c"` and `"a"` beside `"bc"` are the same characters and not the same group.

**A sum of nothing is not zero.** A group where no record filled the column
totals to null. Zero is a sum somebody measured; the count beside it already
says how many rows there were, so nothing is lost and a false zero is not
invented. It is the same rule `row` already applies to an absent field.

## What I would do differently

`Op` covers count, sum, min and max, which is what E10.1 declared. An average is
the obvious fifth and is deliberately absent: it is the first measure that is
not associative, so it cannot be computed slice by slice if a later ticket wants
that, and adding it now would decide that question by accident.

## What I still do not understand

Where a manifest lives is still open, so nothing constructs one and nothing
wires the encoder — the same unknown E10.1 and E10.2 ended on. Nothing here
narrowed it.

## Deliberately not done

**No stale rollup is removed, and no stale slice.** A re-run rewrites the
combined file in place, which is convergent for the rollup on its own, but the
slices under it can still carry a key no record has any more — and the rollup is
computed from the records, not from those files, so it does not read them. That
is E10.4's ticket, and it is now the whole of what E10.4 has left to remove.

**The rollup is not materialised without the slices.** A projection whose source
holds no records writes nothing at all, the combined file included, which is the
rule E10.2 set for an empty projection and this keeps.
