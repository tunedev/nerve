# nrv-ew4 — the context a turn is answered from

`recall.Routes.Context` answers one question: given a workspace and what the
turn says, what should the model be shown? Two halves:

- **The profile.** `memory.Store.Current`, so only what the workspace still
  believes, sorted highest priority first and in written order within a
  priority. The caller passes how many memories it has room for.
- **The documents.** The turn is asked as a question at `<fusion>/query/<turn>`.
  Every key in the fused ranking that sits inside the caller's scope is read
  back from `<corpus>` through the scope's view.

```
$ go test -count=1 -v -run TestATurnsContextHoldsAMemoryAndADocumentThatWerePutIn ./cmd/nerved/
    recall_test.go:68: Context(ada, Postgres) = {Memories:[{Seq:1 Fact:{Kind:preference Body:prefers tabs Priority:normal} ...}] Documents:[{Key:cv Body:Nerve uses Postgres for storage.}]}
--- PASS: TestATurnsContextHoldsAMemoryAndADocumentThatWerePutIn (0.01s)

$ go test -count=1 -v ./internal/core/recall/ | grep -- '--- '
--- PASS: TestATurnsContextHoldsItsMemoriesAndTheDocumentsRetrievedForIt (0.00s)
--- PASS: TestAKeyOutsideTheScopeIsNeverRead (0.00s)
--- PASS: TestTheProfileLeadsWithWhatMattersMost (0.00s)
--- PASS: TestATurnWithASeparatorIsAskedAsItsWords (0.00s)
--- PASS: TestATurnThatAsksNothingIsRefused (0.00s)

$ make check
0 issues.
reproducible: 73c35fae28b59cf14f0c56deb352a3127088b87c0aff0e9a9f73611145edf46c
```

The `cmd/nerved` case runs through `registerKinds`: two graphfs mounts and a
hybridfs over their `/mentions`. A memory goes into the log and a document into
the graphs, and both come back. Before calling the route, the case checks that
the fusion's own answer names Bob's document. So Bob's document is missing from
Ada's context because the route left it out, not because the graph never
ranked it.

Each core case was checked by breaking the code it guards. Four mutations,
four failures:

```
no priority sort      -> TestTheProfileLeadsWithWhatMattersMost: prefers tabs|never force push|...
separators kept       -> TestATurnWithASeparatorIsAskedAsItsWords: turn "how does CI/CD work" contains a separator
no scope filter       -> three cases: read "/ada/hunt/bob/hunt/salary": file does not exist
no clean-key check    -> TestAKeyOutsideTheScopeIsNeverRead: read "/ada/hunt/bob/hunt/salary": file does not exist
```

## What the pattern was

**The route does the join.** The nrv-301 note left one question open: should
the fusion mount grow a path that reads documents, or should the route join a
fused ranking to the corpus? The route does it. hybridfs keeps its thin
contract (a heading, then rows whose first field is a key) and knows nothing
about bodies. The route knows two paths from config and nothing about kinds.
Which rankers stand behind the fusion, and where the corpus is kept, are still
mount config.

**Isolation has two layers, and the last two mutations show both.** The index
covers every workspace, so a ranking can name anyone's document. The first
layer is lexical: a key counts only if its clean form lies under
`Scope.Path("/", "")`. The second is the scope's view of the corpus, which
contains any name it is handed. With the lexical filter removed, the view
still refused to reach Bob's document. It turned it into a not-found under
Ada's own subtree. So the read failed loudly instead of leaking, but the route
would have been unusable. The filter is what makes the route work, and the
view is what makes a filter bug safe.

**Profile means current memories, not a new store.** Nothing in the repo is
called a profile. The memory package doc already names "the context a turn is
built from" as a reader of the log, and says priority exists for "a caller
with limited room". Inventing a profile store would give a second place for
the same facts, with two ways to disagree.

## What surprised me

**hybridfs refuses to fuse a single ranking.** The first acceptance draft
mounted one graph, and the table refused it at boot: "fuses two or more
rankings and was given 1". That is correct, since a fusion of one is just a
ranking. The case now mounts two graphs holding the same documents, which is
what two rankers over one corpus look like.

**A turn is a path segment, and people type separators.** "How does CI/CD
work" would name a place under the query directory rather than ask a
question. The words are what a ranker reads, so separators become spaces. A
turn that is empty, all separators, or `..` after that mapping is refused.

## What I chose not to build

- **A transport.** No RPC method and no composition wiring. The only service
  is `Resources`, and exposing this route means a proto change plus edits to
  `cmd/nerved/main.go` and `api.go`. The bead asked me to keep out of those
  two files. The route is ready for an adapter to hold, just as E7.9's search
  routes and E8.9's lifecycle routes are.
- **Relevance-ranked memories.** The profile is the whole set of current
  memories, cut by priority. It is not ranked against the turn. Ranking
  memories by relevance would mean asking a second ranker, and no index over
  memories exists yet.
- **A bound on documents.** The fusion's `results` setting already bounds
  them, and that setting is mount config.
- **A failing corpus read degrading to a partial answer.** A key that ranks
  but will not read fails the whole call. A context silently missing a
  document looks exactly like a complete one, which is the same reasoning
  `memory.decode` and hybridfs use.

## What I would do differently

Put the scope in the ranking, not only in the filter. The fusion caps its
answer at `results` across every workspace. One user's documents can therefore
crowd another user's out of the top N, and after filtering the second user
gets fewer documents than they should. Nothing leaks, but a busy workspace
makes a quiet one's retrieval worse. The fix belongs where the ranking is
made: a scoped query, or an index per workspace. It does not belong here.

## What I still do not understand

**How documents will actually be keyed once they reach the index.** The
workspace lifecycle names the vector index and the graph as areas whose
workspaces are subtrees, and this route relies on that: a document key starts
with `<user>/<workspace>/`. No `ingest.Indexer` writes into either index yet,
so no real key has ever been checked against this assumption. If the indexer
keys documents differently, every document is filtered out, and the answer is
an empty list rather than an error.
