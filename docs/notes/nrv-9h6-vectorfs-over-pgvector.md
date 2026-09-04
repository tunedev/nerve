# nrv-9h6 — vectorfs over pgvector

A similarity index is a mount. Documents go in by writing paths under `/docs`,
and a question is answered by reading one under `/query`: the answer is the
corpus ranked, closest first, as bytes. `internal/adapters/outbound/vectorfs`
holds the plugin, the `Index` port it consumes and the pgvector side of that
port; the vectors come from `gateway.AsEmbedder()`, so nothing here knows there
is an Ollama.

```
/docs/<key>    the corpus. Writing a document embeds it; reading gives it back
/query/<text>  the question. Reading it answers with the corpus, ranked
/config        what the index is
```

```
$ go test -count=1 -v ./internal/adapters/outbound/vectorfs/ | grep -c '^--- PASS'
15

$ NERVE_VECTORFS_INDEX=... NERVE_OLLAMA_ENDPOINT=... \
    go test -count=1 -v -run 'TestAVectorMount|TestPgvectorAnswers' \
      ./cmd/nerved/ ./internal/adapters/outbound/vectorfs/
--- PASS: TestAVectorMountAnswersThroughTheChainTheConfigNames (0.07s)
ok      github.com/tunedev/nerve/cmd/nerved     0.074s
--- PASS: TestPgvectorAnswersAQuestionWithTheCorpusRanked (0.02s)
ok      github.com/tunedev/nerve/internal/adapters/outbound/vectorfs     0.021s

$ make test-index
ok      github.com/tunedev/nerve/internal/adapters/outbound/vectorfs     1.251s
ok      github.com/tunedev/nerve/cmd/nerved     0.343s

$ NERVE_CONFIG=nerve.json ./bin/nerved | grep -E 'mount|gateway'
{"level":"INFO","msg":"mount","prefix":"/memories","kind":"vectorfs",
 "caps":{"AppendOnly":false,"ReadDestructive":false,"ReadOnly":true,
         "Broadcast":false,"ObjectStore":false}}
{"level":"INFO","msg":"gateway","providers":["embeddings"],"serves":["embed"]}

$ curl -s localhost:7788/config | jq '.mounts[0].settings'
{"dimension": "768", "index_url": "xxxxx", "name": "memories", "neighbours": "3"}

$ go test -race -count=1 ./internal/core/llm/ ./internal/adapters/outbound/vectorfs/ ./cmd/nerved/
ok      github.com/tunedev/nerve/internal/core/llm       1.023s
ok      github.com/tunedev/nerve/internal/adapters/outbound/vectorfs     1.021s
ok      github.com/tunedev/nerve/cmd/nerved     1.244s

$ make lint
0 issues.
```

## What the pattern was

**A query is a path, because the tree has no parameters.** Everything a caller
can say to a mount, it says by naming one, so the question goes where a name
goes and the answer comes back the way content does. That is what "through the
VFS verbs" had to mean; the alternative was a second interface beside the port,
which is the thing the whole architecture refuses.

**queuefs was the template and it fit almost exactly.** Two places in one mount
with different descriptors, a port defined at the consumer, the shipped side and
an in-memory stand-in behind it, keys folded into one level of a tree by the
plugin and never by the store. `Index` is `Journal` verb for verb plus
`Nearest`. Reading a queue consumes and reading a document does not, and that
difference lives entirely in `Capabilities` — `ReadDestructive` for one,
`ObjectStore` alone for the other — which is the descriptor doing its job.

**Two implementations is what makes the ordering a promise.** The in-memory
index computes cosine similarity in Go and pgvector computes it through an HNSW
index, and both are held to the same case: the document about retries comes back
first and the one about pasta last. An order that was only right because there
was one implementation would not have been a port.

**The composition root gained one edge and one line.** The chain is now built
before the mount table, because a mount may be over a model and nothing points
back the other way. `registerKinds` takes the routed `Embedder`, the way it
already takes the keyspace: a thing two mounts might share is the root's to own.

## What surprised me

**The dimension question answered itself once the column was written.** E7.2
left it open, expecting mount config and the model to disagree with nothing
checking. They do disagree, and the check turned out to belong at the plugin's
edge rather than at boot: the mount compares the width of every vector it is
handed against the one its column was declared with and refuses a mismatch
naming both numbers. A boot probe was the obvious alternative and it is wrong —
it would make a mount refuse to come up because a model endpoint was down, which
is exactly what the gateway exists to stop mattering.

**An empty document needed a decision, not a special case.** `Create` has to
make an entry, conformance says so, and there is no vector for text that is not
there. Storing a NULL and skipping the nulls in the ranking is the honest
answer: a document with nothing to say has no place in a similarity, and the
descriptor did not have to be bent to say it. It also means conformance drives
the plugin through every verb without calling a model once.

**One table per index, because a width is part of a column's type.** queuefs
puts every queue in one table and scopes by name; two indexes over two models
have two widths and cannot share a column. So the name becomes an identifier,
which no parameter can carry, which is why `naming` refuses anything that is not
a bare lowercase word — checked rather than escaped, before any statement is
built from it.

**The vector goes over the wire as text.** `$1::vector` with `[1,2,3]` in it
needs no driver extension, so nothing in the build depends on `pgvector-go` to
store a vector. It is the same move `queuefs` makes with a mode: spell it the
way the column reads it, at the adapter's edge.

## What I would do differently

Ship the embedding stand-in E7.2 asked for. Its absence is now visible rather
than theoretical: `bagEmbedder` in this package's tests is exactly the hashing
embedder that note wanted, and it lives in `_test` where the build cannot reach
it, so a laptop with no Ollama running has a `vectorfs` mount that refuses to
boot. Filed rather than smuggled in.

## What I still do not understand

**Whether `Stat` of a question should cost what answering it costs.** It does:
a question has no stored existence, so the only true size is the length of the
answer, and getting it means asking. That is honest and it means a caller that
stats before reading pays for two ANN queries. A cheaper lie was available and
was not taken; whether the port wants a third answer here — "describable only by
reading" — is not this ticket's to invent.

**How `hybridfs` will read this.** E7.8 fuses this ranking with a graph's, and
what it has to work with today is a TSV it would parse back out of a `Read`.
Either that is the composition the tree offers and RRF over two parsed rankings
is fine, or the fusion wants a port that both indexes satisfy — in which case
`Index.Nearest` is already most of it and the question is where it should live.
E7.10's `DocStore` may settle it first.

**Whether the score belongs in the answer at all.** RRF needs only the order,
and a score that is comparable within one answer and meaningless between two is
a number a reader can misuse. It is there because an answer with no scores is
hard to debug, which is not the same as needing them.
