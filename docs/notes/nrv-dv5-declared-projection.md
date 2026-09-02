# nrv-dv5 — a declared projection

`internal/core/projection` is the manifest a projection is. A document names the
area its rows come from, the file that makes a directory a record, the columns
to read out of it, the column that cuts the rows into slices, and what those
slices roll up into. `SliceSchema` and `RollupSchema` derive both tables from
that manifest, so no schema is written anywhere — here or in whatever
materialises one.

```
$ go test -race -count=1 ./internal/core/projection/ -v | grep -c -- '--- PASS'
36

$ go test -race -count=1 ./internal/core/projection/
ok      github.com/tunedev/nerve/internal/core/projection        1.017s

$ make lint
0 issues.
```

## What the pattern was

**The architectural thesis, applied to a table.** A mount table makes a storage
kind config rather than code; a library catalog does the same for a document
kind. A manifest does it for a column: adding one is an edit to a document, and
no type in this repo changes. The test that proves it appends a `timestamp`
column and a `max` over it to a manifest the package has never seen, and reads
both derived schemas back.

**The rollup schema is derived, not declared, which is the sharper half.** A
manifest that spelled out its rollup columns would be a second schema kept in
step with the first by hand — the thing the ticket refuses. So the rollup names
operations over slice columns and the types follow: a count is an `int` whatever
it counted, and everything else keeps the type it reduced, because the sum of
two sizes is a size.

**Column order is the manifest's, never a map's.** E10.4 asks a re-run to
produce the same result. A schema whose order came from ranging over a map would
make one manifest materialise two ways, and the convergence proof would fail on
something that has nothing to do with the engine.

## What surprised me

**A record's identity is not inside it.** The first draft had one origin for a
column: a field of the record's document. Then the first real record shape —
`meta.json` under `/resources/<user>/<workspace>/<name>/` — turned out to say
nothing about its own name, because the name is the directory. A projection that
could only read fields produces a table whose rows cannot be told apart. Hence
two origins, `field` and `name`, and the same is true of a curated edge and a
library document: this tree puts identity in the path everywhere.

**An empty rollup is the useful default, not a missing one.** A projection whose
slices are already the table people want still has to say what the combined
table is, and declaring neither a grouping nor a measure says exactly that: the
rollup is the union, and its schema is the slice schema. That cost one guard
rather than a second manifest field to mean the same thing.

## What I would do differently

The slice column has to be a discrete value because it becomes part of a slice's
file name, so a float is refused. That is a rule about the materialised layout
enforced in the format, one ticket before the layout exists. If E10.2 names
slices some other way the rule is wrong, and it will be wrong quietly — nothing
fails, a projection is just refused that need not have been.

## What I still do not understand

Where a manifest lives. It decodes from bytes here and nothing reads or writes
one, which was deliberate — the format is one unknown and the storage is
another. But the choice between a manifest as process config, beside mounts and
libraries, and a manifest as a document a workspace keeps in its own subtree is
not obvious, and it decides whether a projection is something an operator
declares for everyone or something a workspace declares for itself.

## Deliberately not done

No engine. E10.2 materialises a slice through DuckDB, and nothing here reaches a
filesystem, so the manifest is a value and the engine that takes one is an
adapter. No `Encode`: manifests are written by hand and read by the process, and
a writer with no caller is a second spelling to keep in step.

No `Catalog` of manifests. One would be the fourth registry in the repo, and the
question it answers — which projections a build has, and whether a name may
repeat — belongs with the decision about where a manifest lives.
