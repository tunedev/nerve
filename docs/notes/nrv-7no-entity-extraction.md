# nrv-7no — entity and relation extraction

`llm.EntityExtractor` is the port and `internal/adapters/outbound/nounlink` is
the first endpoint behind it. Whatever builds a graph holds
`gateway.AsEntityExtractor()`, which is a routed verb over whichever endpoints
config listed; putting a model in front of the laptop's reader is one line in
`providers`.

```
$ NERVE_CONFIG=nerve.json ./bin/nerved | grep gateway
{"level":"INFO","msg":"gateway","providers":["summaries","facts","entities"],
 "serves":["summarise","extract","entities"]}

$ curl -s localhost:7777/config | jq '.providers'
[
  {"name": "summaries", "kind": "termsum",  "settings": null},
  {"name": "facts",     "kind": "cuefact",  "settings": null},
  {"name": "entities",  "kind": "nounlink", "settings": null}
]

$ go test -count=1 -v ./internal/core/llm/ ./internal/adapters/outbound/nounlink/ ./cmd/nerved/ \
    -run "TestEntityExtractionReachesTheProviderThatReadsIt|TestACapitalisedRunIsOneEntity|TestAPhraseBetweenTwoNamesIsARelation|TestTheLaptopChainReadsEntities"
--- PASS: TestEntityExtractionReachesTheProviderThatReadsIt (0.00s)
--- PASS: TestACapitalisedRunIsOneEntity (0.00s)
--- PASS: TestAPhraseBetweenTwoNamesIsARelation (0.00s)
--- PASS: TestTheLaptopChainReadsEntities (0.00s)

$ go test -count=1 -v ./internal/adapters/outbound/nounlink/ | grep -c '^--- PASS'
13

$ make fmt-check && go vet ./... && go test -race ./... && make lint
0 issues.
```

## What the pattern was

**The third capability cost what the second one cost.** `Embedder`,
`FactExtractor` and now `EntityExtractor` are the same four pieces: a narrow
interface, an accessor on `Provider` and on `NoAccessors`, an accessor on
`Gateway` that resolves the chain once, and a routed type whose one method hands
`route` a closure. `serving` and `route` are generic over the accessor, so
failover, per-provider circuits, per-attempt deadlines and error classification
arrived already written. There is still one failover rule in the process.

**The verb is `ExtractEntities` and not `Extract`.** `FactExtractor.Extract`
took the name first, and a hosted endpoint that reads both facts and entities is
one type implementing both interfaces — Go gives it one method per name. The
asymmetry in the port is the price of the collision not existing later, when the
type that hits it is an adapter somebody is writing against a live model.

**`Extraction` carries both halves with an invariant between them.** Every
entity a relation names is also in `Entities`, so a caller makes its nodes from
one field and its edges from the other in one pass. Returning relations alone
and expecting a caller to derive the nodes would make "what did this document
name" unanswerable for a document that named things and related none of them,
which is most documents.

## What surprised me

**Capitalisation is not the hard part; the first word of a sentence is.** A run
of capitals is a name, except that every sentence starts with one. The rule that
does the work is a stopword list of sentence openers, and the run that was only
its opener is no mention at all — so "The pipeline" is nothing and "Nerve
indexes them" is `Nerve`. The alternative, dropping every sentence's first word,
loses the commonest place a document names its own subject. `cuefact`'s note
worried that a cue list quietly becomes the specification; `starters` is the
same shape of risk and is why the package doc says out loud that an entity which
only ever opens a sentence is missed.

**Punctuation carries as much as capitalisation.** "Nerve, Postgres" and "Nerve
Postgres" are the same two capitalised words in a row, and only the comma says
they are two names rather than one two-word name. That is a field on the token —
whether the word had punctuation after it — and without it the first list in any
document becomes one entity nobody wrote.

**The sentence loop is load-bearing for headings, not for full stops.** A full
stop leaves no words between two mentions, so a cross-sentence relation could
not form anyway. What the per-sentence loop actually buys is the heading: "Nerve
Architecture" followed by a paragraph starting "Postgres" is two names only
because `termsum.Sentences` ends a paragraph that never finished. That is the
test worth having, and the first version of it asserted something that was true
for a different reason.

**Predicates match the whole span or not at all.** Matching a cue anywhere
between two mentions would relate two names either side of a subordinate clause
that said nothing about them. Requiring the phrase to be the whole span, with
articles dropped so `uses a` is `uses`, keeps every edge to a claim the sentence
made — at the cost of "Nerve uses Postgres and RabbitMQ" relating only the first
pair, which is written down in the package doc rather than patched around.

## What I chose not to build

No ingest stage and no sidecar. E7.5 is the graph that consumes this, and what
it wants written down is a question about the graph's storage rather than about
the port; a stage added now would be guessing at the shape and would have to be
rewritten by the ticket that actually reads it. What is wired is the provider
kind, so the banner reports `entities` on a laptop with no model.

No entity kinds beyond `mention`. A capital says a word names something and
nothing about what. Deciding that "Postgres" is a database is what a model is
for, and a kind invented from a suffix list here would be a guess the graph then
had to trust.

No coreference, no normalisation, no aliasing. "Nerve" and "the substrate" are
two things to this extractor, and E7.5 will find that out. Merging them needs
either a model or a synonym table, and a synonym table is the trap `cuefact`
already named.

## What I still do not understand

Whether `Relation` should carry what it was read from. A graph built from many
documents will one day be asked why it believes an edge, and the sentence is the
answer; the port does not carry one, because `Fact` does not either and the two
should not disagree about that without a reason. E8.7 wants the same thing for
facts, so whatever is decided there should decide this.

Whether the entity kind belongs on the entity at all, or on the extraction. Every
entity one endpoint returns is of that endpoint's vocabulary, and a graph mixing
two chains' vocabularies is the same failure as an index mixing two models'
vectors — the `Embedder` doc calls that the operator's line to hold, and nothing
here says the equivalent because I do not yet know whether it is true.
