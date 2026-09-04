# nrv-49r — a question becomes a statement, and the statement is checked

E10.7. `projection.Asker` answers a question about a workspace's rollup in the
words whoever asked it used: it reads the one combined file the slices roll up
into, describes that table to `gateway.AsSQLWriter()`, checks the statement the
model wrote, and runs it through a `Querier`. `duckparquet.Querier` is the
DuckDB side of that port, and `ollama.Writer` is the model side of the other
one.

```
$ go test -count=1 -v ./internal/core/projection/ | grep -c -- '--- PASS'
100

$ NERVE_DUCKDB_BIN=duckdb go test -count=1 -v ./internal/adapters/outbound/duckparquet/ | grep -c -- '--- PASS'
23

$ go test -count=1 -v ./internal/core/llm/ | grep -c -- '--- PASS'
26

$ go test -count=1 -v ./internal/adapters/outbound/ollama/ | grep -c -- '--- PASS'
28

$ NERVE_DUCKDB_BIN=duckdb go test -race -count=1 ./internal/adapters/outbound/duckparquet/ ./internal/core/projection/
ok      github.com/tunedev/nerve/internal/adapters/outbound/duckparquet 1.192s
ok      github.com/tunedev/nerve/internal/core/projection        1.027s

$ make lint
0 issues.
```

With no DuckDB anywhere, nineteen of the twenty-three skip; with no Ollama,
three of the ollama package's skip. Neither is needed to run the suite.

The end-to-end case materialises the applications projection with the real
engine and then asks it a question:

```
which stage has more than one role on it, and what is it worth
  -> SELECT stage, payroll FROM applications WHERE roles > 1
  -> stage VARCHAR, payroll HUGEINT
  -> applied | 90000
```

Two applications are at `applied`; the salaries add up to the one that was
stated. The rollup answered, not the slices.

## What the pattern was

**The table reaches the statement as a CTE, so a statement names the projection
and never a file.** The engine writes the Parquet into a temporary directory and
prefixes `WITH "applications" AS (SELECT * FROM read_parquet('table.parquet'))`,
which is why a statement has to begin with `SELECT`: one that opened with a CTE
of its own could not be wrapped, only spliced. The rule the core enforces and
the shape the adapter needs turned out to be the same rule, and it is the
cheaper one to state — a caller can check "starts with SELECT" by looking.

**The check is in the core, before the engine, and it is three rules.** One
statement, so nothing rides along behind a semicolon; a `SELECT`, so the one
statement cannot write; and no name that reaches outside the table, so the one
read is the rollup. A model is writing code against data it has never been
shown, and those three make the worst it can do a refusal.

**`Querier` is the second port E10.3 said a reading engine would need.** It is
separate from `Encoder` rather than a verb on it, because a build is entitled to
write its tables with one thing and read them back with another. Both carry
`Extension`, so neither has to be told what the other is.

**The gateway gained a capability and no caller changed.** `AsSQLWriter` is the
fourth accessor, the routing is the same `serving`/`route` pair the other three
use, and the endpoint that writes SQL is a line of config. A build with none of
them reads that back from `Capabilities` where it wires the query path, rather
than on the first question somebody asks.

**One Ollama, two models.** What embeds a corpus and what writes SQL are
different models, so the kind takes `model` and `sql_model` and serves the
capabilities it was given a model for. A provider that serves one of them
returns a real nil from the other accessor rather than a typed one — an
interface holding a nil pointer is not nil, and the chain would have routed to
an endpoint that has nothing.

## What surprised me

**DuckDB prints a number as text when it will not fit in a JSON one.** A `sum`
over integers is a `HUGEINT` and comes back as `"210000"`; a `DECIMAL` comes
back quoted too. So a cell is read from whichever spelling arrived rather than
from the JSON type, and the DuckDB type in the description is what says which
projection type it is.

**A JSON object has no column order, so the answer needs `DESCRIBE` beside it.**
The run asks two questions — the described columns, then the rows — and the two
answers arrive on stdout in order. The description is the only thing that gives
the row objects an order and a type; without it, `SELECT a, b` and `SELECT b, a`
are the same answer.

**A timestamp is printed in the session's zone, and not every zone is a whole
number of hours.** Tokyo caught nothing — `+09` parses, and the instant compares
equal whatever offset it was spelled with. Kolkata is what the test uses now:

```
$ TZ=Asia/Kolkata go test ./...   # with the session zone left alone
row 0, column "seen": "2026-01-02 08:34:05+05:30" is not an instant: invalid argument
```

So the session is put in UTC before anything is read, which also settles the two
spellings one instant would otherwise have.

**Prose is worth handing back.** The writer takes the markdown fence off, since
a model fences code however plainly it is asked not to and a fence would be a
statement refused for a reason that is not the real one. It strips nothing else:
a model that explained itself has not answered with a statement, and the caller
should see that rather than have it quietly patched into something runnable.

## What I would do differently

The refused names are a list — `attach`, `copy`, `install`, `read_*` and the
rest. A list is a thing to keep adding to, and the honest version of this check
is a parser that knows which names in a statement are table references. That is
a lot of machinery for a rule that only has to hold behind a `SELECT` with no
semicolon in it, so the list stands, and this is the paragraph that says it is a
floor rather than a sandbox.

## What I still do not understand

Where a manifest lives is still open, which is where E10.1, E10.2 and E10.3 all
ended. Nothing constructs one, so nothing wires the materialiser or the asker in
`main`, and `NewAsker` has no composition root to be called from yet.

## Deliberately not done

**No mount.** A question is a path everywhere else in this repo — vectorfs
answers one by reading `/query/<text>` — and a projection asked the same way
would be the obvious next thing. It is not this ticket: E10.7 is the translation
and the run, and a mount over it is a shape the roadmap has not asked for.

**No workspace scope.** The asker takes a root and a manifest, which is what the
materialiser takes. The rollup is already one table per workspace, and a scope
on one of the two and not the other would be two answers to where a projection
lives.

**No model on the laptop that writes SQL.** The extractive summariser and
cuefact exist because a stage needed a floor that runs on a plane; there is no
such floor for SQL, and the deterministic one that would fit — a template per
question shape — answers a different question from the one asked. So the live
test skips unless `NERVE_OLLAMA_SQL_MODEL` names a model that has been pulled,
and everything below the model is asserted without one.
