# nrv-7kk — multi-hop traversal inside graphfs

`/neighbours/<name>` now answers with every claim within `depth` hops of the
entity, not only one. `depth` is a mount setting beside `edges`, defaults to 1
(so an existing mount answers as it did), and reads back from `/config`. No new
component: the walk runs over the edges graphfs already derives from its
documents. The answer carries a `hop` column, so a reader can tell a claim about
the entity from a claim about its neighbour.

```
mount: {"name": "notes", "depth": "2"}   extractor: nounlink, through the gateway
texts: "Nerve uses Postgres for storage." / "Postgres runs on Disk." / "Furiosa owns Rig."

$ go test -count=1 -v -run TestAGraphMountOfDepthTwoAnswersTwoHopsOut ./cmd/nerved/
    graphs_test.go:61: Read(/graph/neighbours/Nerve) =
        from	from_kind	predicate	to	to_kind	support	hop
        Nerve	mention	uses	Postgres	mention	1	1
        Postgres	mention	runs-on	Disk	mention	1	2
--- PASS: TestAGraphMountOfDepthTwoAnswersTwoHopsOut (0.00s)

$ go test -count=1 -v -run 'TestATwoHop|TestTheBoundApplies|TestAClaimReachedTwice|TestTheDepthIsRead' \
    ./internal/adapters/outbound/graphfs/
--- PASS: TestATwoHopExpansionReachesTwoHopsAndNoFurther (0.00s)
--- PASS: TestTheBoundAppliesToTheAnswerNotToEachHop (0.00s)
--- PASS: TestAClaimReachedTwiceIsReportedOnceAtItsNearestHop (0.00s)
--- PASS: TestTheDepthIsReadBackFromTheConfig (0.00s)

$ make fmt-check && go vet ./... && go test -race -count=1 ./... && make lint
0 issues.
```

Disk, two hops out, comes back. Rig does not, and the same case reads
`/neighbours/Furiosa` and finds Rig there, so the absence is not an extractor
that never saw it.

## What the pattern was

**A bound applies to the answer, not to the walk.** The walk gathers every
claim within `depth` untruncated, orders the lot, and cuts to `edges` once. The
order is support first and hop second. Hop first would have looked safer and
been useless: an entity with more than `edges` direct claims would answer at
depth 2 exactly as at depth 1, which is the truncation E7.7 found in the
client-side composition. `TestTheBoundAppliesToTheAnswerNotToEachHop` is the
discriminating case: temporarily capping each hop at `edges` made it fail with
`Model` missing, and restoring the answer-level bound made it pass.

**One corpus walk per hop, not per neighbour.** A hop asks for the claims
touching any entity in the frontier set, so depth 2 is two walks of the
documents whatever the fan-out. The composed version E7.7 measured was 1 + k.

**A claim is reported once, at its nearest hop.** The edge back to where the
walk came from is the edge it came along.

## What surprised me

**The first run through the real wiring returned one hop and passed nothing
but the compile.** `Postgres writes Disk pages.` made no edge: nounlink reads a
fixed phrase list, and `writes` is not on it. The graph was correct; the text
said nothing nounlink could read. It looked exactly like a traversal that
stopped at one hop. Checking the extractor's phrase list before touching the
walk is what told the two apart.

## What I chose not to build

- **Kind filtering.** Left out, deliberately. A walk matches by name at every
  hop, so a name two kinds share joins both neighbourhoods beyond it; the
  package doc says so. nounlink types everything `mention`, so no case can show
  a collision today. What forces it: the first extractor with real kinds,
  plus a case where a shared middle name pulls an unrelated neighbourhood into
  a two-hop answer. Then filter by the kind of the node reached, not the name.
- **A depth per read.** Depth is the mount's, as `edges` is. A second mount
  over the same graph name with another depth is config; a path parameter is a
  tree that grew parameters.
- **The Postgres relations table, and any change to the write path.** Per
  E7.7.

## What I would do differently

Put the `hop` column in when `/neighbours` was first built. Adding a column
changed every exact-string case in the package; the heading was always going
to need it.

## What I still do not understand

**Whether an orphaned far claim helps a retriever.** Support-first ordering
can keep `Ollama runs Model` (hop 2) while cutting `Nerve uses Ollama` (hop 1),
so the answer holds a claim whose link to the entity is not in it. The `hop`
column says it is two out, but not through what. Whether that is noise or
signal depends on what reads it, and nothing reads it yet.

**Cost at depth.** Each hop is a full walk of the documents, so depth 3 over
E7.7's 5,000 documents should be about three times one walk. Not measured.
