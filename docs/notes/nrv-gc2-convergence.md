# nrv-gc2 — running it again is running it once

`Materialise` now converges a projection on what its records say rather than
adding to what earlier runs left. Slices this run did not write are removed from
the projection's directory, and a projection whose source has emptied has its
directory and its combined file taken away.

Against DuckDB v1.5.5 named by `NERVE_DUCKDB_BIN`:

```
$ go test -count=1 -v ./internal/adapters/outbound/duckparquet/ | grep -c -- '--- PASS'
14

$ go test -count=1 -v ./internal/core/projection/ | grep -c -- '--- PASS'
88

$ make check
...
reproducible: 77d19ecfe21d92aa1dfae31cb7461adc41f3d55792f9630e87653dee8e4ff237
```

That hash is of the binary built at the commit before this note was added to it.
`repro` builds HEAD in two clean clones and requires them equal, and the toolchain
stamps the revision into the binary, so the property is the `cmp` and never the
digest: no note can quote a digest of the commit that carries it.

With no DuckDB anywhere, eleven of the fourteen skip and `make check` is green.

The proof is one end-to-end case. It materialises the three applications, reads
every file the run wrote, materialises again and asserts the same paths hold the
same bytes; then deletes the record at the offer stage, materialises a third
time, and asserts the offer Parquet is gone and the rollup reads back only what
is left:

```
applied|2|90000|2026-01-04 07:00:00+00
```

Each half fails without the change: the second run leaves `offer.parquet` behind
because nothing removed it.

## What the pattern was

**Determinism was already there; convergence was not.** Slices come back sorted,
groups are totally ordered, and the encoder writes the same bytes twice — E10.2
and E10.3 each did their half, and re-running over unchanged records was already
a no-op. What was missing is the other half of the same claim: a re-run over
records that *have* changed has to leave what those records say and nothing an
earlier run said. Determinism is about the function; convergence is about the
tree it writes to.

**A derived directory is defined by what belongs in it, not by what was put
there.** `prune` reads the projection's directory and removes every entry this
run did not write, matching on the paths written rather than on what a slice is
called. Filtering by the encoder's extension would have made a build that swaps
encoders carry the old one's files forever.

**Emptying is a state, not an absence of one.** The empty case used to return
early, which was correct as long as nothing had ever been written. Converging
means it now removes.

## What surprised me

**The convergence tests one layer down do not add up to this one.** Slices are
the same on every run, the rollup is the same on every run, the encoder's bytes
are the same on every run — three green tests, and the whole was still divergent,
because none of them looks at the tree. The property is about the writing, and
every test was about the values.

**A test that never fails proves nothing, and that is easy to miss.** The
end-to-end case passed on the first attempt to break it, and the reason was an
automatic WIP checkpoint commit: `git checkout --` restored the fix rather than
removing it. Reverting to the merge commit's version made both halves fail as
they should.

## What I would do differently

`prune` reads the directory on every run. For a projection with a few hundred
slices that is one ReadDir against a write per slice, so it is not worth
avoiding; for one with a hundred thousand it would be, and the answer then is
almost certainly to write slices under a run-scoped prefix and rename, not to
keep a list of what was written last time.

## What I still do not understand

Where a manifest lives is still open — the same unknown E10.1, E10.2 and E10.3
ended on. Nothing here narrowed it.

## Deliberately not done

**Convergence is not atomic.** A run that fails partway through has written some
slices and pruned nothing, so the tree is neither the old state nor the new one.
The next successful run converges it, which is the property this ticket asks
for, but a reader between the two sees a mix. Making the swap atomic wants a
staging directory and a rename, and a rename of a directory is a verb the mount
table does not promise across mounts.

**Nothing removes a projection whose manifest is gone.** `prune` scopes itself to
the one projection it was given, so a manifest that is deleted rather than
emptied leaves its tables standing. Removing them needs a list of the manifests
a build has, which is the question of where manifests live.
