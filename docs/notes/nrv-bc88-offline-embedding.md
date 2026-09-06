# nrv-bc88 — the laptop's embedder

`termvec` is `termsum`'s counterpart for embeddings: a bag-of-words hashing
embedder behind `llm.Embedder`, so a build with no Ollama running still has
something behind `gateway.AsEmbedder()` and a `vectorfs` mount comes up.

```
$ NERVE_CONFIG=nerve.json ./bin/nerved | grep gateway
{"level":"INFO","msg":"gateway","providers":["summaries","vectors"],"serves":["summarise","embed"]}

$ curl -s localhost:7777/config | jq '.providers'
[
  {"name": "summaries", "kind": "termsum", "settings": null},
  {"name": "vectors", "kind": "termvec", "settings": {"dimension": "256"}}
]
```

Postgres up, Ollama not running — which is the case this ticket exists for:

```
$ docker compose ps --services
minio
postgres

$ NERVE_VECTORFS_INDEX=$NERVE_POSTGRES_DSN go test -count=1 -v \
    -run 'TestALaptopWithNoModelStillMountsAnIndex|TestAVectorMountAnswersThroughTheChainTheConfigNames' ./cmd/nerved/
=== RUN   TestAVectorMountAnswersThroughTheChainTheConfigNames
    vectorindex_test.go:51: set NERVE_OLLAMA_ENDPOINT to run against an Ollama; `make up` starts one
--- SKIP: TestAVectorMountAnswersThroughTheChainTheConfigNames (0.00s)
=== RUN   TestALaptopWithNoModelStillMountsAnIndex
--- PASS: TestALaptopWithNoModelStillMountsAnIndex (0.02s)
ok      github.com/tunedev/nerve/cmd/nerved     0.030s

$ go test -count=1 -v -run TestConformance ./internal/adapters/outbound/termvec/
--- PASS: TestConformance (0.00s)
    --- PASS: TestConformance/one_vector_per_text (0.00s)
    --- PASS: TestConformance/every_vector_is_one_width (0.00s)
    --- PASS: TestConformance/the_same_text_embeds_the_same_way_twice (0.00s)
    --- PASS: TestConformance/no_texts_is_no_vectors (0.00s)

$ make test-index
ok      github.com/tunedev/nerve/internal/adapters/outbound/vectorfs     1.446s
ok      github.com/tunedev/nerve/cmd/nerved     1.629s

$ make lint
0 issues.
```

## What the pattern was

**E7.2 predicted the size of this and was right.** Its note said a hashing
embedder "would be a package plus a line in `registerProviders`", and that is
what it was: `termvec.Embedder`, a `Provider` embedding `llm.NoAccessors` with
one accessor overridden, and one line in the composition root. Nothing in
`internal/core` moved, and the second endpoint serving one capability needed no
change to the routing that already served the first.

**The width is where the two ends of a config meet.** `Embed` reports no width
and cannot, so a `vectorfs` mount is sized by its own `dimension` setting.
`termvec` spells its setting the same way for that reason: an operator naming
768 in one and 256 in the other has written a config that the plugin's edge
catches on the first document — `index %q holds vectors of %d and was handed one
of %d`. Nothing checks it at boot, and that is E7.2's open question still open,
now with two ways to get it wrong instead of one.

**Refuse a setting this kind does not take.** `ollama` already did, and here it
earns its place a second time: `dimension` is the one number that has to agree
with something outside the provider, so a misspelled `dimensions` that defaulted
quietly would surface as a mount refusing every document with a sentence naming
neither the key nor the typo.

**The conformance question is answered.** `internal/llmtest` is to model
endpoints what `internal/vfstest` is to storage plugins: `RunEmbedder` holds an
embedder to one vector per text, in the order given, one width, and the same
answer twice. `termvec` runs it with nothing started; `ollama`'s live test runs
it against a real model and lost the hand-rolled copy of the same four
assertions. One suite, two implementations, no second set of cases to disagree
with the first.

## What surprised me

**A vector of zeros is not a poor answer, it is not an answer.** The obvious
implementation counts words into buckets and hands back what it counted, and a
text whose words the tokeniser all dropped — `"?!"`, a single letter — counts
nothing. Cosine distance against a vector with no direction is NaN, and
Postgres sorts NaN last in an `ASC` ordering, so the document would not rank
badly, it would rank nowhere and quietly. The whole text is hashed as one word
in that case. Empty text is the only vector of zeros left, and the mount does
not embed empty text at all.

**"Poor and honest" has a limit and this is it.** A bad ranking is honest. A
number the ranker cannot compare is a defect wearing the same clothes, and the
difference only showed up by asking what pgvector does with the vector rather
than what the embedder does with the text.

**The most useful thing this package says is what it is not for.** Vectors are
comparable only with vectors from the endpoint that made them, so a chain
listing `termvec` behind `ollama` fills one index from two vector spaces the
first time Ollama falters — the trap `llm.Embedder` names as the operator's
line to hold, now reachable with two registered kinds and no code change. It
went in the package doc and in `registerProviders`, because nothing in the
process can detect it.

## What I would do differently

Nothing about the shape. If the width came up again I would still default it:
no width is the right one, buckets are a budget against collisions rather than a
property of a model, and requiring the number would not have made an operator
any more likely to match the mount.

## What I still do not understand

**Where the vector's dimension is owned.** E7.2 left this open and shipping a
second embedder has not closed it, only made the disagreement cheaper to write.
The mount could check the width of the first vector it is handed against the
column it declared, which would move the failure from the first document to the
first document rather than to boot — no better. Refusing at boot means probing
an endpoint at boot, which is what the gateway exists to avoid.

**Whether a bag of words is enough for E7.8's fusion.** Reciprocal Rank Fusion
takes ranks and not scores, so it needs no calibration between rankers — but it
does assume each ranker's order means something. A hashing embedder and a graph
walk fused together may be one ranker's answer with noise stirred in. The test
of that is a corpus and a question set, and neither exists yet.

**Whether `llmtest` wants the other three capabilities.** Summarisation,
extraction and SQL-writing have one implementation each, so there is nothing for
a suite to hold two things to. The suite exists now because embeddings have two;
adding cases for a capability with one would be writing the second copy this
pattern is meant to prevent.
