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

Scaffolding only. Nothing is implemented — the binary compiles, prints its version and
exits. The roadmap is the work.

## Running the stack

Everything runs locally, free, with no cloud account. If it does not work on a plane, it is
a bug.

```
make up      # Postgres with pgvector, RabbitMQ, Ollama
make check   # formatting, lint, tests
make run
make down
```

`make help` lists the rest. Copy `.env.example` to `.env` if you want to change anything;
the defaults match the compose file and need no edits.

## Layout

```
cmd/nerved/          composition root; the only file that knows every concrete type
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
