# nrv-kj7 — one path to a model

`internal/core/llm` is the gateway. A model endpoint is a `Provider`, a build's
endpoints are an ordered chain that is itself a `Provider`, and what a caller
holds is a routed verb rather than a client. The chain comes from config in the
order it is asked, so the acceptance — no plugin holds a provider client — is
structural: there is nowhere else to hold one.

```
$ NERVE_CONFIG=nerve.json ./bin/nerved | grep gateway
{"level":"INFO","msg":"gateway","providers":["laptop"],"serves":["summarise"]}

$ curl -s localhost:7911/config | jq '.providers'
[{"name": "laptop", "kind": "termsum", "settings": null}]

$ go test -count=1 -v ./internal/core/llm/ | grep -c '^--- PASS'
16

$ go test -race -count=1 ./internal/core/llm/ ./internal/config/ \
    ./internal/adapters/outbound/termsum/ ./cmd/nerved/
ok      github.com/tunedev/nerve/internal/core/llm       1.024s
ok      github.com/tunedev/nerve/internal/config 1.016s
ok      github.com/tunedev/nerve/internal/adapters/outbound/termsum      1.013s
ok      github.com/tunedev/nerve/cmd/nerved     1.251s

$ make lint
0 issues.
```

## What the pattern was

**The mount table, one altitude up.** A `Gateway` is an ordered set of named
providers that implements `Provider` itself, the way `MountTable` implements
`FileSystem`; a `Registry` maps a kind name onto a factory so an endpoint is a
line of config; a capability is reached through an accessor that returns a
narrow interface or nil, because every caller holds the router and
`gateway.(Embedder)` would interrogate the router. Nothing about that shape is
new here. Finding it already written down was most of the ticket.

**Order is the routing table.** There is no second document saying which
provider serves which capability. A capability is served by the providers whose
accessor answers, in the order config listed them, and the first to answer is
the answer. That deletes the table that would otherwise have to agree with the
providers, and it makes "put the hosted model in front of the laptop's" a moved
line.

**Classification is the whole of failover.** Which failure moves on to the next
endpoint is one predicate: a transient failure the attempts could not outlast,
or a circuit already open. Anything else is an answer — a request one model
refused is refused by the next model too, and asking again is a slower failure
that spends a second dependency on the way. `vfs.Classify` already sorted errors
into those two classes for the storage side, and reusing it meant the gateway
inherited the judgement instead of making a second one.

**The stability values were already generic; only their entry points were not.**
`Retry.repeat` and `circuit.guard` were unexported because the only caller was a
`FileSystem` decorator. Exporting them as `Retry.Do` and `Circuit.Guard` was the
source-level fix. The alternative — a retry loop and a circuit written beside
them in `llm` — would have been two answers to when a failure is worth another
attempt, and they would have drifted.

## What surprised me

**A method expression on an interface is exactly the accessor-resolver.**
`Provider.AsSummariser` is a `func(Provider) Summariser` with no closure and no
name, which is what a generic walk over the chain needs to be handed. It made
`serving` and `route` one shape each rather than one per capability, so E7.2's
embeddings and E7.4's extraction are an interface, an accessor and a five-line
routed type — no second copy of the failover rule.

**`ErrNoProvider` was unreachable and had to go.** The accessor answers nil when
no endpoint in the chain serves the capability, so a routed call cannot start
with an empty chain. Keeping the sentinel meant keeping a branch no test could
reach, and the honest fix was to resolve the accessor once, in the accessor, and
hand `route` a chain that is non-empty by construction. The nil is the better
answer anyway: a build wiring a pipeline finds out there is no summariser where
it wires it, not on the first document.

**Redaction needed two declarations, not one.** `Effective` took one
`PublicSettings`, and adding providers to the operator view tempted a single
lookup over both tables. A provider kind and a storage kind sharing a name would
then inherit each other's declaration — a key one table called safe, echoed for
the other. Two parameters, sixteen call sites in tests, and a test that fails if
they are ever merged again.

## What I would do differently

Ship the accessor for `Embedder` too, or say plainly in the roadmap that E7.1 is
the routed path and E7.2 is its first real endpoint. The ticket title names three
capabilities and this lands one, because the other two have no implementation and
`Extract` has no signature anyone can write honestly before E7.5 says what an
entity is. That is the right call under "no layer that cannot be run", but the
ticket reads as though it were not.

## What I still do not understand

Whether `Retry`, `Breaker` and `Classify` should still live in `vfs`. Two
domains now import them for the stability vocabulary, and `llm` importing the
storage package to get a circuit reads as backwards even though the dependency
is core-to-core. The move is mechanical — a `stability` package under
`internal/core`, and `vfs` keeps the four decorators — but it touches `config`,
the mount registry, the composition root and their tests, which is a bigger
change than the ticket that noticed it. Filed rather than done.

Whether a provider should carry its own timeout. One `provider-timeout` covers
the chain, and the case it does not cover is real: a hosted endpoint and a
laptop model are not slow in the same way, and a deadline set for the slower one
makes the faster one's failure take as long. The settings map is already there
to carry it, so this is a question about whether the second endpoint wants it,
and there is only one endpoint.

Whether the gateway should be measured the way a mount is. Every call through
every mount produces a span, a duration and a log line; a call to a model
produces nothing, and "why is it slow" is a question about models more often
than about filesystems. `vfs.Recorder` is written in the storage domain's
vocabulary — a mount, a verb, a path — so it is not the thing to reach for, and
what the second one should be labelled by is not obvious to me yet.
