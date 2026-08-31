# Nerve

A storage and memory substrate for AI agents.

Agents forget. Everything an agent knows between one session and the next has to live
somewhere, be retrievable cheaply, and stay isolated from every other agent's version of
the same thing. Nerve is the layer that answers that, once, for everything above it.

## The idea

One interface, a mount table, and implementations that describe themselves.

Every kind of storage — local disk, object store, vector index, knowledge graph, job queue,
key-value store — is a plugin mounted at a path, reached through the same small set of
verbs. A vector search is a write of a document and a read of a query path. A queue pop is
a read that happens to consume. The verb set does not grow to accommodate either.

What varies is not the interface but what each implementation says about itself:
append-only, read-destructive, read-only, broadcast. Callers ask; they never inspect a
concrete type to find out what something can do. Capability is answered per path, because
it genuinely differs per path.

The payoff is that adding a new kind of storage is configuration rather than code — no
switch to extend, no factory to edit, no caller to teach.

## What it will do

| Capability | What it means |
|---|---|
| Uniform storage | Disk, object store, SQLite, key-value, all behind one verb set |
| Versioning | Content-addressed history: commit, diff, restore, reconcile |
| Ingestion | Documents in, summarised at three levels of detail, indexed in the background |
| Retrieval | Vector similarity and graph traversal, fused into one ranking |
| Memory | Sessions promoted into durable facts, deduplicated, reconciled, valid over an interval |
| Registries | Versioned, searchable document registries — agent skills being the first |
| Projections | Declare a columnar view; a convergent engine materialises it |
| Sandboxing | A scoped filesystem an agent's code cannot escape |

`docs/roadmap.md` holds all of it as tickets, grouped into twelve epics, including the
things deliberately refused.

## Status

Scaffolding, plus the first three storage kinds. The binary boots on a validated config,
mounts whatever the config names, prints the mount table it resolved, and serves a
read-only operator surface until it is signalled, then drains. `memfs` keeps a tree in
memory: a plain read-write tree that claims no descriptor, which is what makes it the
reference every later plugin is read against. `localfs` passes the same verbs through to a
host directory. `kvfs` is a namespaced key-value store over one keyspace, and the first
kind that claims a descriptor: object-store, so a name is a prefix and nothing is a
directory. `queuefs` is a job queue: read-destructive under its job path and read-only one
path up, and the first kind whose durable state is a Postgres row and whose broker is only
transport. The roadmap is the work.

## Running the stack

Everything runs locally, free, with no cloud account. If it does not work on a plane, it is
a bug.

```
make up      # Postgres with pgvector, RabbitMQ, Ollama — waits until each one answers
make observe # Grafana with Tempo, Prometheus and Loki behind an OTel Collector
make status  # what is running, and whether it is healthy
make run     # the binary, against that stack
make check   # formatting, lint, tests under the race detector
make down    # stop, keeping the data
```

`make lint` needs no linter installed: `golangci-lint` is a `go.mod` tool dependency, so
the repo pins its version and builds it with the repo's toolchain. CI runs the same target.

`make help` lists the rest; `make logs` follows the stack; `make clean` also deletes the
stack's data. That data lives in Docker volumes, not in the working tree, so removing it is
`down -v` rather than a `rm` that trips over files the containers own.

Copy `.env.example` to `.env` if you want to change anything. Both `make up` and `make run`
read it, so a setting is spelled once. If one of the published ports is already taken on
your machine, change `NERVE_POSTGRES_PORT`, `NERVE_RABBITMQ_PORT`, `NERVE_RABBITMQ_UI_PORT`
or `NERVE_OLLAMA_PORT` — the URLs below them follow, and no versioned file is edited.

## Configuration

Four layers, later winning: defaults, then the JSON file `NERVE_CONFIG` names, then the
environment, then flags. A setting is spelled once — `log-level` is the flag, the JSON key,
and `NERVE_LOG_LEVEL`. `nerved -h` lists them with the values it would actually use.

An invalid setting stops the boot and every problem is reported at once, so a bad config
costs one restart rather than one per mistake. The file path is an environment variable
rather than a flag because the file layer sits below the flag layer.

What the process actually resolved is readable rather than guessable: `admin-addr`
(`127.0.0.1:7777` by default) serves `GET /config`, which answers with every setting under
that same single name, and with the mount and library tables. Any password inside a connection string is
replaced first, in the view rather than at the surface, so a second surface cannot forget.
Nothing there is writable — config changes go through the layers and a restart.

The same surface answers two probes, both from memory and neither touching a dependency:
`GET /livez` says the process is running, and `GET /readyz` says it is taking traffic. They
are separate because the questions are. A probe that dialled Postgres would report
Postgres's health as this process's, and one bad minute on the database would then restart
a process that was working. On a signal, readiness starts refusing while liveness keeps
passing, so whatever routes traffic here stops sending before the server stops answering
and whatever restarts crashed processes leaves this one alone. `drain-delay` (`1s`) is how
long the listener stays open refusing readiness before it closes; set it to a probe period
or two where something is actually routing, and to `0s` where nothing is.

The mount table lives in that file too: `mounts` is a list of `{prefix, kind, settings}`,
and a mount appears by adding a line rather than by editing code. A kind the build does not
have stops the boot naming the ones it does. Mounts come from the file alone, because
neither an environment variable nor a flag carries a list.

So does the library table: `libraries` is a list of `{kind, file, root}`, and a kind of
document — a skill, a blueprint, a checklist nobody has written code for — appears by
adding a line. A document is a directory holding the file its kind names, and the root is
where that kind's mount was put, so where a library sits is a mount and what it holds is
the kind. Two kinds may share one root; two libraries of one kind stop the boot.

Every mount is bounded. `call-timeout` is the deadline each verb carries before it reaches
a plugin, because a mount may be remote and no caller of the tree can tell. It is a ceiling
rather than an extension — a caller with less time left keeps its own — and no spelling of
the setting asks for an unbounded call: zero and negative stop the boot. Opening a stream
is a call and is bounded like one; the transfer it returns is not, and runs until the
caller closes it or cancels.

A transient failure is attempted again; nothing else is. `retry-attempts` and
`retry-backoff` say how many attempts a call gets and how long the waits between them are —
random up to a ceiling that doubles, so callers that failed together do not come back
together. Two things have to be true before an attempt is repeated, and the second is the
one usually skipped: the error is one a plugin marked transient, since a validation error,
a missing entry or a refusal is an answer that another attempt cannot change; and the call
can be made twice, which `Remove`, `Rename` and `Symlink` cannot, nor a read of a
read-destructive path or a write to an append-only one. The bound sits inside the retry, so
each attempt is a call with its own deadline.

A mount that keeps failing stops being called. `breaker-threshold` (`5`) is how many
transient failures in a row open its circuit, and `breaker-cooldown` (`30s`) is how long it
is then left alone before one call goes through to find out whether it is back — an answer
resumes traffic, a failure starts the wait again. Only a transient failure counts: a
missing entry is a healthy no, and a mount answering those is a mount that is working. Each
mount has its own circuit, so a broken one refuses its own subtree at once while the rest
of the tree answers as before, and it keeps describing itself throughout — `Capabilities`
reaches no far end. The breaker sits between the bound and the retry, so it counts attempts
rather than sequences and the retry stops the moment it opens.

A streaming read runs with a fetch in front of it. `readahead-window` (`262144`) is how
many bytes are pulled off a plugin before the caller has asked for them, so the next piece
is arriving while the caller works on the one it has: over a source costing 2ms a piece,
the first read drops from 2.14ms to 0.008ms and a 512KiB stream from 70.8ms to 36.8ms. It
is speculative, and a caller that stops early threw away at most a window. A path a read
consumes is never read ahead of, because what the queue holds when the caller closes is
dropped and there that would lose the job. The fetch sits under the bound, so what the
idle bound counts is still the caller's reads; `0` turns it off.

Every call through every mount is measured, so "why is it slow" is a question the
telemetry already answers. One call becomes three things that share a trace: a span
naming the mount, the kind, the verb and the path; a measurement in
`nerve.mount.call.duration`, labelled by mount, kind, verb and outcome but never by path,
because one series per file is not a series; and a log line saying the same thing. What is
timed is the call the caller made, retries and backoff included — the wrapper sits outside
those, since a call that took three seconds across three attempts is not three fast calls.
The outcome label is the class the retry policy already sorted the error into, so a mount
that is failing says whether another attempt would have helped.

`telemetry-endpoint` is where that goes: an OTLP collector URL, `http://` for plaintext and
`https://` for TLS, as the OTLP exporters read the standard variable. It is empty by
default and empty means nothing is exported — the recorder still runs, into providers that
discard, which is the same code path a build exporting everything takes. `make observe`
starts a backend to point it at and prints the URL. Logs always reach stderr as JSON at
`log-level`, collector or no collector, because a collector that is down is exactly when
they are wanted.

## Layout

```
cmd/nerved/          composition root; the only file that knows every concrete type
internal/config/     the layered config the composition root boots on
internal/core/       entities, rules, and the ports the core owns
internal/adapters/   inbound transports and outbound implementations
docs/roadmap.md      the work, as tickets
docs/specs/          designs, before implementation
docs/notes/          what each increment taught
```

Dependencies point inward, always. The core declares interfaces; adapters satisfy them;
nothing in `internal/core` imports a driver. That rule is enforced by `depguard` in
`.golangci.yml` rather than by good intentions, so violating it fails `make lint` with a
message naming the reason.

## Principles worth stating once

- A port exists where a real alternative exists. If the second implementation cannot be
  named, it is not a port — it is an interface tax.
- Capability is discovered by asking, never by type assertion. Every caller holds the mount
  dispatcher rather than a plugin, so an assertion would interrogate the wrapper and never
  the thing that would do the work.
- Tests are a confidence marker, not a coverage statistic. Green means shippable.
- Configuration is layered and validated at startup. Nothing operationally interesting is
  hardcoded.
