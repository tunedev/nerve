# nrv-d0x — embedding over Ollama

`llm.Embedder` is the port and `internal/adapters/outbound/ollama` is the first
endpoint behind it. A stage that wants vectors holds `gateway.AsEmbedder()`,
which is a routed verb over whichever endpoints config listed; nothing outside
the adapter knows there is an Ollama, and swapping the endpoint is one string in
`providers`.

```
$ NERVE_CONFIG=nerve.json ./bin/nerved | grep gateway
{"level":"INFO","msg":"gateway","providers":["laptop","embeddings"],"serves":["summarise","embed"]}

$ curl -s localhost:7777/config | jq '.providers'
[
  {"name": "laptop", "kind": "termsum", "settings": null},
  {"name": "embeddings", "kind": "ollama",
   "settings": {"base": "http://localhost:11434", "model": "nomic-embed-text"}}
]

$ NERVE_OLLAMA_ENDPOINT=http://localhost:11434 \
    go test -race -count=1 -run TestLive ./internal/adapters/outbound/ollama/
ok      github.com/tunedev/nerve/internal/adapters/outbound/ollama       11.723s

$ go test -count=1 -v ./internal/adapters/outbound/ollama/ | grep -c '^--- PASS'
15

$ go test -race -count=1 ./internal/core/llm/ ./internal/adapters/outbound/ollama/ ./cmd/nerved/
ok      github.com/tunedev/nerve/internal/core/llm       1.024s
ok      github.com/tunedev/nerve/internal/adapters/outbound/ollama       1.019s
ok      github.com/tunedev/nerve/cmd/nerved     1.235s

$ make lint
0 issues.
```

## What the pattern was

**E7.1 predicted the size of this and was right.** Its note said embeddings
would be "an interface, an accessor and a five-line routed type", and that is
what `internal/core/llm` gained: `Embedder`, `AsEmbedder` on `Provider` and on
`NoAccessors`, `Gateway.AsEmbedder`, and an `embedding` struct whose one method
hands `route` a closure. `serving` and `route` are generic over the accessor, so
failover, per-provider circuits, per-attempt deadlines and error classification
arrived already written. No second copy of the failover rule exists to disagree
with the first.

**Capability routing needs no table.** The build now has two providers that
serve disjoint capabilities — `termsum` summarises and embeds nothing, `ollama`
embeds and writes no sentences — and each verb reaches the endpoint that has it
because `serving` walks the chain and keeps only the providers whose accessor
answered. `TestEachCapabilityReachesTheProviderThatServesIt` asserts the
summariser is never asked for a vector.

**The batch is the port's shape, not the adapter's convenience.** `Embed` takes
and returns a slice because a corpus is many documents and a model is remote:
one vector at a time pays the round trip once per document. `/api/embed` is
natively a batch, so the port's shape and the wire's agree.

**Translate at the adapter's edge.** The gateway's failover predicate is one
line — `vfs.Classify(err) == Retryable`, or a circuit already open — so the
whole of what the adapter has to do is put the right sentinel on Ollama's status
codes, the way `httpfs` does. That is a `switch` in `statusError` and nothing
else.

## What surprised me

**The interesting decision was which failure is *not* worth a second endpoint.**
A model the endpoint has not pulled answers 404. Marking that transient would
have made a chain quietly fall through to the next endpoint and embed with
whatever model *it* runs — vectors that are numerically fine, land in the index,
and are comparable with nothing already in it. It is `fs.ErrNotExist` and it
stops the chain. Summarisation has no equivalent trap: a summary from the second
model is still a summary.

**Failover is only correct because a chain is meant to be one model twice.**
`ProviderSpec` already said "two endpoints of one kind is the case the chain
exists for", and for embeddings that stops being a convenience and becomes the
correctness condition. A chain listing two *different* embedding models is a
config that fills one index from two vector spaces, and nothing in the process
can detect it. That went in the `Embedder` doc comment as the operator's line to
hold, because it is the one thing about this port that cannot be enforced from
inside it.

**Positional pairing is the whole contract, so it is checked.** Vectors are
matched to texts by index and by nothing else. An endpoint answering a different
count has already lost the pairing, and handing that on files each document
under another document's meaning — so a mismatch is `fs.ErrInvalid` rather than
a shorter slice.

## What I would do differently

Ship the embedding stand-in alongside. `termsum` exists so summarisation runs on
a plane; embeddings have no such fallback, so a laptop without Ollama running
has a `providers` list that boots fine and an `AsEmbedder` that fails on every
call. A hashing or bag-of-words embedder would be a poor index and an honest
one, and it would make the conformance question below answerable. It is not in
this ticket's acceptance and it is not here.

## What I still do not understand

**Where the vector's dimension is owned.** `Embed` does not report it, because a
routed chain has no single answer to give and the port would be lying if it
pretended otherwise. E7.3's pgvector column needs the number at DDL time, so it
will come from mount config — and then two places name a dimension and nothing
checks they agree. The mismatch surfaces as a Postgres error on the first
insert, which is loud but late. Whether the index should verify the width of the
first vector it is handed, or the mount should refuse at boot, is E7.3's call
and I have not made it.

**Whether providers want a conformance suite.** `internal/vfstest` exists
because storage plugins make promises a test can hold them to. Two providers
that serve one capability make the same promises — one vector per text, in
order, one width — and there is currently one provider per capability, so there
is nothing to share a suite with. The second embedding endpoint is the evidence
that would settle it.

**Whether an embedding call should be measured.** E7.1 left this open for the
gateway and it has got sharper, not softer: embedding a corpus is thousands of
calls where summarising is one per document, and "why is indexing slow" is the
question that will be asked. `vfs.Recorder` speaks of mounts, verbs and paths,
so it is still not the thing to reach for.
