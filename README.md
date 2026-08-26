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

Scaffolding only. The binary boots on a validated config, prints the mount table it
resolved, and serves a read-only operator surface until it is signalled. No storage kind
is implemented yet. The roadmap is the work.

## Running the stack

Everything runs locally, free, with no cloud account. If it does not work on a plane, it is
a bug.

```
make up      # Postgres with pgvector, RabbitMQ, Ollama — waits until each one answers
make status  # what is running, and whether it is healthy
make run     # the binary, against that stack
make check   # formatting, lint, tests
make down    # stop, keeping the data
```

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
that same single name, and with the mount table. Any password inside a connection string is
replaced first, in the view rather than at the surface, so a second surface cannot forget.
Nothing there is writable — config changes go through the layers and a restart.

The mount table lives in that file too: `mounts` is a list of `{prefix, kind, settings}`,
and a mount appears by adding a line rather than by editing code. A kind the build does not
have stops the boot naming the ones it does. Mounts come from the file alone, because
neither an environment variable nor a flag carries a list.

Every mount is bounded. `call-timeout` is the deadline each verb carries before it reaches
a plugin, because a mount may be remote and no caller of the tree can tell. It is a ceiling
rather than an extension — a caller with less time left keeps its own — and no spelling of
the setting asks for an unbounded call: zero and negative stop the boot.

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
