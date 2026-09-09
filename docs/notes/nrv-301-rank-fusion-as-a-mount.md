# nrv-301 — hybridfs, rank fusion as a mount

Two rankers disagree and a fusion decides. `hybridfs` reads a ranking from each of
the paths its config names, folds them by Reciprocal Rank Fusion, and answers at
`/query/<question>` like any other mount.

```
/query/<text>  the question. Reading it answers with the fused ranking
/config        what the fusion is: name, damping, results, sources
```

A key's standing is the sum, over the rankers that named it, of 1/(`damping` + its
rank there). Nothing else about a ranking is read — not a similarity, not a claim
count, not how many rows came back — which is the acceptance criterion held by
construction rather than by tuning.

`graphfs` gained the ranking it had no way to offer: `/mentions/<name>` answers with
the texts that name an entity, the ones stating most claims about it first. Without
it the two halves of "vector and graph" ranked different universes — document keys
on one side, entity names on the other — and RRF over those is an interleave, not a
fusion.

```
$ go test -count=1 -v ./internal/adapters/outbound/hybridfs/ | grep -c '^--- PASS'
24

$ go test -count=1 -v -run 'TestAFusion|TestMentions' \
    ./cmd/nerved/ ./internal/adapters/outbound/graphfs/
--- PASS: TestAFusionAnswersFromTheMountsItIsMountedBeside (0.01s)
--- PASS: TestAFusionInTheTableRefusesAWrite (0.00s)
ok      github.com/tunedev/nerve/cmd/nerved     0.014s
--- PASS: TestMentionsRankTheTextsThatSayMostFirst (0.00s)
--- PASS: TestMentionsIncludeATextThatOnlyNamesTheEntity (0.00s)
--- PASS: TestMentionsOfAnUnknownEntityAnswerWithNoRows (0.00s)
--- PASS: TestMentionsAreBoundedByTheEdgesSetting (0.00s)
ok      github.com/tunedev/nerve/internal/adapters/outbound/graphfs      0.003s

$ make fmt-check && go vet ./... && go test -race ./... && make lint
0 issues.
```

## What the pattern was

**A contract thin enough that no plugin had to be named.** A ranking is a heading
line and then rows, best first, whose first tab-separated field is the key. That is
everything `hybridfs` requires. It does not know what a `vectorfs` is, it does not
parse the columns, and the second column of each ranking — a cosine distance in one,
a claim count in the other — is never read. Fusing a third kind of index is mounting
it somewhere that answers a question with a ranking.

The heading is dropped rather than validated, which was a deliberate choice against
the safer-looking one. A fusion that checked for `key\tscore` would have needed
editing to accept `key\tclaims`, and the next ranker would have needed another edit.
What is uniform across rankers is the order and the key; what their own columns are
called is theirs.

**The blast radius of "which instance?"** The obvious wiring was the one `proxyfs`
and `exportfs` already use: take the registry's bare `Build`, construct the sources
from pass-through settings, own them. That is wrong here, and the reason is
`graphfs` specifically. A graph is held whole in memory and loaded from its blob at
`Open`. A privately built second graph would answer from the blob as it stood at
boot and never see a text written through the mounted one — a fusion that silently
goes stale, with no error anywhere and no way to tell from the answer.

So the sources are read *through the tree*: `hybridfs` holds a `func() vfs.FileSystem`
and reads `<source path>/<question>` from the mount table it is itself in. The
function is the whole of the cycle-breaking. `cmd/nerved/hybrid.go` is a struct with
one field, filled the line after `registry.Table(mounts)` returns, and nothing calls
it until a question is asked.

## What surprised me

**The two notes that came before had already framed this decision, and both options
they named were wrong in the same place.** `nrv-9h6` wrote: "either that is the
composition the tree offers and RRF over two parsed rankings is fine, or the fusion
wants a port that both indexes satisfy". `nrv-uy6` added the part that mattered — "a
hit is now a key into a store both rankers could share".

Reading the second one is what showed the first was underspecified. The choice is
not *how* the fusion reaches its rankers; it is *what the rankers rank*. Both of the
options as written would have fused a document ranking with an entity ranking, and
neither the port nor the parse would have made that mean anything. A `Ranker` port
both plugins satisfied would have type-checked perfectly and produced a fused list
where nothing ever overlapped.

The unit that had to be settled first was the key. Once it is "a document key both
rankers can name", the graph needs a texts ranking, and once it has one, the parsed
tree composition is enough — no port required.

**A tie-break turned out to be a fairness rule.** Sorting by score alone left two
keys each ranked first by one ranker in map order. Adding "then the best rank any
ranker gave it, then the source that named it" makes the order reproducible, and it
also fixes which ranker wins a genuine tie: the first one listed in config. That is
an operator's decision, spelled in the order they wrote their sources, rather than
Go's map iteration making it per-process.

## What I would do differently

**Give `graphfs` its own bound for the mentions.** It reuses `edges`, which is
documented as the claims one expansion carries. The number is fine for both — a
context window's worth — but they are two different quantities and one setting now
means both. It was a setting's worth of churn in another ticket's plugin, and I
chose not to spend it. A second bound is the honest shape.

**Refuse a source that names the fusion itself.** `exportfs` refuses an `exportfs`
behind it and `proxyfs` refuses a `proxyfs`; a `hybridfs` whose source path points
into its own subtree recurses until the stack goes. It cannot be caught the same
way, because a mount does not know its own prefix — the table knows where it put it.
Config that names it is config that crashes the process at the first question, which
is late and loud rather than at boot and quiet. Not fixed, and worth a ticket.

## What I still do not understand

**Whether a failing ranker should fail the read.** It does. A fused answer missing
one of its rankings is the answer to a different question, and nothing in the rows
says which rankers stood behind them, so a caller cannot tell. That reasoning is
sound and it still trades away the resilience Tenet 2 asks for: a model endpoint
being down currently takes the fusion with it, when the graph alone would have been
a useful degraded answer. The fix is not error handling — it is an answer that can
say which rankers answered, and that is a change to the shape of the report rather
than to the failure path. Deliberately left, because inventing that shape here would
be inventing it for `vectorfs` and `graphfs` too.

**Whether `damping` should be per-source.** One constant treats every ranker as
equally trustworthy, which is the property that makes fusion need no calibration.
Per-source weights would let an operator say the index is worth more than the graph
— and would put the calibration straight back, under a different name. I believe
the single constant is right and I have no evidence, because the corpus that would
settle it does not exist yet.

**What E8.2 will actually want.** It builds prompt context from profile and hybrid
retrieval, so it needs the documents behind the keys, not the keys. Whether that is
this mount growing a path that reads them, or the route joining a fused ranking to
the corpus itself, is a question about where the join belongs and not one this
ticket could answer.
