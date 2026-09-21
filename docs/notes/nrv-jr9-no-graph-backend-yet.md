# nrv-jr9 — no graph backend yet

**Decision: (a). No dedicated graph backend.** A two-hop query already answers
through the graphfs that exists, and the limit the numbers show is in how a
graph is written, not in how it is walked. E7.6 is rescoped to multi-hop
traversal with a configurable depth inside graphfs.

**The second implementation, named:** a relations table in the Postgres the
stack already runs (`pgvector/pgvector:pg17`), traversed with `WITH RECURSIVE`.
It sits behind the same mount, and it is the one alternative that also runs in
hosted prod, where the stack is only the app and Postgres.

## The evidence

A throwaway test in the graphfs package, not committed, composed two hops out of
the existing `/neighbours` read: expand the entity, then expand every neighbour
it named. With the stub extractor the graphfs tests use:

```
corpus: "Nerve uses Postgres\nPostgres writes Disk\nFuriosa drives Rig"
twoHop("Nerve"):
    Nerve     mention  uses    Postgres  mention  1
    Postgres  mention  writes  Disk      mention  1
--- PASS: TestEvidenceTwoHopThroughWhatIsBuilt (0.00s)
```

Disk is two hops out and comes back; Rig is disconnected and does not. That is
E7.6's acceptance, answered with no new component.

Cost, same test, in-memory store, five relations per document:

```
docs=1000 relations=5000  two-hop=4.6ms  rows=90 one-write=4.2ms  blob=1006KiB
docs=5000 relations=25000 two-hop=15.3ms rows=90 one-write=19.5ms blob=5173KiB
--- PASS: TestEvidenceCostAtSize (50.72s)
```

A two-hop query is 1 + k walks of the corpus, where k is at most the `edges`
bound. At 5,000 documents that is 15ms. The write is what grows: every write
serialises the whole graph, so one write costs time in proportion to the corpus
and ingesting a corpus is quadratic. The 50 seconds is almost all ingest.

## What the pattern was

**A traversal is not a backend.** E7.6 was written as "real graph backend,
multi-hop traversal, configurable hop depth", which bundles two things. The
traversal is a breadth-first walk over edges graphfs already derives; the
backend is a different place to keep the edges. Only the first is needed to
answer a two-hop query, and only the second is needed when the corpus
outgrows one blob. Splitting them is one unknown per increment.

**The second implementation is chosen by prod, not by features.** The hosted
stack is the app and Postgres, on a free tier. Neo4j Community, Memgraph or an
Apache AGE build would all run on the laptop and none of them would run there,
so any of them would make graph retrieval a laptop-only feature. A relations
table is one more table in a database both stacks already have.

## What surprised me

**The composed query is correct but truncated.** Each `/neighbours` read is
capped at `edges`, so composing reads only follows the best-supported twenty
neighbours out of the first hop. That is the case for doing the traversal
inside graphfs rather than as a client-side pattern: inside, the bound applies
to the answer, not to every hop along the way.

**The bound the E7.5 note left unmeasured is now measured.** Its note said the
whole-blob write would stop being fine at a corpus size nobody had measured.
At 5,000 documents it is 19ms and 5MiB per write on an in-memory store; a real
store adds the I/O of writing 5MiB. That is the trigger for the Postgres
implementation, not a traversal requirement.

## What I chose not to build

Nothing. This bead is a decision. The traversal is E7.6's.

Not a Neo4j, Memgraph, AGE or embedded graph engine port. None has a second
use in this system, and none runs in hosted prod.

Not the Postgres relations table. It becomes worth building when a real
corpus's write latency says so, and the move is behind a mount, so it costs a
new plugin and a config change, not a change to any caller.

## What I still do not understand

**Where a real corpus sits on that curve.** The measurement uses synthetic
documents of five relations each and an in-memory store. A real extractor's
relations per document and a real store's write cost will move the numbers,
and no real corpus has been measured yet.

**Whether the traversal should cross kinds.** Matching is by name alone, as
E7.5 decided. At two hops a name collision compounds: one ambiguous name in the
middle joins two unrelated neighbourhoods. `nounlink` types everything
`mention`, so the first extractor with real kinds is what will show whether
depth needs kind filtering.
