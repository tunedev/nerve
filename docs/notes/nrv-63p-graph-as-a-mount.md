# nrv-63p — graphfs, a graph as a mount

A knowledge graph is a mount. Texts go in by writing paths under `/docs`, and what
the corpus says about a thing comes back by reading its name under `/neighbours`:
the answer is every claim one hop out of it, best supported first.
`internal/adapters/outbound/graphfs` holds the plugin and the one port it
consumes; the entities and relations come from `gateway.AsEntityExtractor()`, so
nothing here knows there is a nounlink or a model behind it.

```
/docs/<key>        the texts. Writing one reads it into the graph
/neighbours/<name> the expansion: one hop out of every entity of that name
/config            what the graph is
```

```
$ go test -count=1 -v ./internal/adapters/outbound/graphfs/ | grep -c '^--- PASS'
27

$ go test -count=1 -v -run 'TestAGraphMount|TestTheLaptopChainAnswersAnExpansion' \
    ./cmd/nerved/ ./internal/adapters/outbound/graphfs/
--- PASS: TestAGraphMountAnswersThroughTheChainTheConfigNames (0.00s)
--- PASS: TestAGraphMountedAgainOverTheSameCorpusKeepsWhatItLearnt (0.00s)
ok      github.com/tunedev/nerve/cmd/nerved     0.007s
--- PASS: TestTheLaptopChainAnswersAnExpansion (0.00s)
ok      github.com/tunedev/nerve/internal/adapters/outbound/graphfs      0.002s

$ make fmt-check && go vet ./... && go test -race ./... && make lint
0 issues.
```

## What the pattern was

**vectorfs was the template and the differences are the interesting part.** Two
places in one mount with different descriptors, a query that is a path because
the tree has no parameters, keys folded into one level of a tree by the plugin,
and the conformance suite run twice — once at the writable texts and once at the
read-only shape above them. What changed is where the state is: an index keeps
its documents in one store and its vectors in another, and a graph keeps
everything it has in one blob. That collapses the two-port ordering problem
vectorfs spends three doc comments on. There is no verb here that can half
happen.

**The blob is the record, and that is one function.** `commit` copies the
document map, applies the change to the copy, writes the whole graph to the
store, and only then makes the copy the graph this mount answers from. A save
that failed is a change that did not happen, in memory as well as in the store,
and the case that proves it reads the mount back after a refused write. The
alternative — change memory, then try to persist — is the shape that leaves a
process answering things no restart can reproduce, and it costs the same number
of lines to avoid.

**A boot probe is right here and wrong for a model.** vectorfs's note argues
against probing at boot, because a mount that refuses to come up when an
endpoint is down defeats the gateway. The graph loads its blob in the factory
anyway, and for the reason that makes the two different: an endpoint that is
down costs an answer, while a store that is down would cost the graph — a mount
that came up empty because a bucket blinked would overwrite the real graph on
its first write. The same rule, applied to what is actually at risk, points
opposite ways.

**Support is what makes the bound honest.** An expansion has no score to sort
by, so the order was going to be alphabetical and the `edges` bound was going to
truncate arbitrarily. Counting how many texts state each claim gives an order
worth cutting: the claims a corpus keeps repeating survive. It also gives E7.8
something to fuse that is not a similarity.

## What surprised me

**Deduplicating claims is what makes it a graph.** Two documents saying "Nerve
uses Postgres" is one edge standing on two, not two edges. Until that fold
happens the mount is a log of extractions with a filter on it, and the shape of
the answer says so: rows repeat and nothing tells a reader why. The fold is four
lines and it is the difference between the two things.

**The adjacency index is the part not to build.** The documents are the record,
so an index beside them is a second structure to keep true across write, rename,
removal and load. A graph small enough to hold whole is small enough to walk, so
`claimsAbout` walks it. That is a real bound — it is linear in the corpus per
question — and it is written down rather than hidden, because the ticket after
this one is the graph backend where it stops holding.

**Two methods, not eight.** The corpus is a `vectorfs.DocStore` with eight verbs,
and what a graph needs of it is `Load` and `Save`. Declaring the narrow port at
the consumer and adapting the wide store at the composition root keeps graphfs
from importing vectorfs at all, and means the plugin never sees the listing and
moving verbs it has no documents to use them on.

**A namespace was needed and a guarantee was not available.** A graph named
`notes` and a vector index named `notes` are two mounts over one keyspace, so
the root files graphs under `graphs/`. That is a namespace, not a proof: an
operator who names an index `graphs` gets what they asked for, which is the same
bargain `kvfs` already makes and is said out loud rather than defended against.

## What I chose not to build

No multi-hop and no configurable depth. E7.6 is the graph backend and that is
where a traversal belongs; a second hop here would be a nested walk of the same
map, and shipping it would make the backend ticket a rewrite rather than a
replacement.

No persistence port with a second implementation and no snapshots. E7.11 asks
for exactly that, and what it will want to extract is `Store` plus a way to name
a point in time. What is here is the one-blob version it will generalise.

No coreference and no aliasing. "Nerve" and "the substrate" are two entities,
which nrv-7no predicted this ticket would find out. It did: an expansion of a
name misses every claim made about the same thing under another name, and
nothing here can fix it without either a model or the synonym table `cuefact`
already named as a trap.

No entity listing. There is no `/entities` place naming everything the corpus
mentioned, because nothing has asked for one and a listing of a graph is a
different question from an expansion of a node.

## What I still do not understand

**Whether matching by name alone is right.** `llm.Entity` is a (Kind, Name) pair
and the port says the pair is an address, but a path carries one string and a
caller has a name rather than a vocabulary. So an expansion of `Postgres`
answers for every kind of thing called Postgres, and the kind columns in the
answer are what tell them apart. That is defensible while `nounlink` types
everything `mention`; the first extractor with real kinds is what will say
whether it stays defensible.

**Whether an edge should carry what it was read from.** nrv-7no left this open
for `Relation` and it is sharper here: the graph knows which document stated each
claim, and `support` is a count of exactly that, but the answer names no
document. A caller asking why the graph believes an edge cannot follow it back.
Adding the keys is easy; deciding whether provenance belongs in the port or only
in this mount is what E8.7 should settle for facts and this should follow.

**Whether the whole-blob write survives a real corpus.** Every write serialises
the entire graph, which is fine for a laptop's notes and obviously wrong for
anything larger. The number where it stops being fine is unmeasured, and the
honest thing is that E7.6 arrives before it matters rather than that it does not
matter.
