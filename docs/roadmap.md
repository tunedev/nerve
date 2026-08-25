# Nerve roadmap

Every capability Nerve owes, once each. A capability that is refused is still a ticket,
because the refusal is the deliverable.

Nothing here is deferred. Graph traversal and sandboxed execution are in scope.

## Cross-cutting constraint (settled)

Capability discovery uses **descriptors plus accessors**, never type assertion.

```go
type FileSystem interface {
    // 10 core verbs

    Capabilities(path string) Caps   // descriptors: data, varies per path
    AsSymlinker() Symlinker          // extra verbs: narrow interface, or nil
    AsStreamer()  Streamer
    AsTruncater() Truncater
}
```

Rationale, in one clause: every caller holds the mount dispatcher rather than a plugin, so
`fs.(Symlinker)` interrogates the wrapper and never the thing that would do the work — the
same defect that forced `http.ResponseController` into the standard library. Accessors on
the base interface make the dispatcher forward, and descriptors are per-path because
capability genuinely varies within one mount.

Applies to every ticket in E1. Plugins embed a `NoCaps` default so a plugin implements only
what it supports.

---

## E1 — The port and the mount table

| ID | Ticket | Done when |
|---|---|---|
| E1.1 | `FileSystem` interface: create, mkdir, remove, remove_all, read, write, read_dir, stat, rename, chmod | All ten verbs defined in the core, no driver imported |
| E1.2 | `Caps` descriptors: append-only, read-destructive, read-only, broadcast, object-store; resolved per path | A queue mount reports read-destructive on its job path and not on its config path |
| E1.3 | Accessor set: `AsSymlinker`, `AsStreamer`, `AsTruncater`, with a `NoCaps` embeddable default | A plugin supporting nothing optional compiles with zero accessor code |
| E1.4 | Mount table: longest-prefix resolution, itself implementing `FileSystem` | Dispatcher forwards both descriptors and accessors to the resolved plugin |
| E1.5 | Plugin registry: a mount is added by config, not by editing a switch or factory | A new mount appears from a config file with no recompile |
| E1.6 | Architecture boundary declared with `depguard`: `internal/core/...` may not import an adapter or a driver | A driver import added to a core package fails lint, with the tenet named in the `desc` message |
| E1.7 | **Skip** stateful open handles and a `touch` verb | Recorded as skipped; neither has a caller, and a port with no second implementation is an interface tax |

## E2 — Storage plugins

| ID | Ticket | Done when |
|---|---|---|
| E2.1 | `memfs` — in-memory, the reference implementation and test substrate | Full conformance suite passes against it |
| E2.2 | `localfs` — host disk passthrough | Conformance suite passes |
| E2.3 | `sqlfs` — SQLite-backed tree | Conformance suite passes |
| E2.4 | Object store plugin behind one port; MinIO locally | Object-store descriptor set; prefix semantics, no true directories |
| E2.5 | `kvfs` — namespaced key-value for sessions and agent state | Conformance suite passes |
| E2.6 | Plugin conformance suite: one test set every plugin runs, honouring declared descriptors | Adding a plugin means running the suite, not writing a new one |
| E2.7 | Utility plugins: `httpfs`, `proxyfs`, `heartbeatfs`, `serverinfofs`, `devfs`, `hellofs` | Each mounts and answers `stat` |
| E2.8 | Object-backed key-value, sharing the `kvfs` port | Second KV implementation nameable |

## E3 — Streams and queues

| ID | Ticket | Done when |
|---|---|---|
| E3.1 | `queuefs` over RabbitMQ, read-destructive, with a transactional outbox | Losing the broker loses no durable state; a new consumer can be backfilled from Postgres |
| E3.2 | `streamfs` — ring-buffer append stream, implements `Streamer` | Reader observes writes without polling the whole file |
| E3.3 | `streamrotatefs` — rotating log stream | Rotation is observable through `read_dir` |
| E3.4 | Work bus over the same broker — do not build a second distribution abstraction | Publish and handle paths exercised by a crash-simulation test |
| E3.5 | `WriteObserver` hook: a VFS write emits a work event | Indexing triggers on write, not on the caller remembering to ask |
| E3.6 | Completion notification for awaiting async work | Caller can block on a job finishing without polling |
| E3.7 | Export refresh: cache invalidation after a materialisation | Stale export is not served after its inputs change |

## E4 — Versioning

| ID | Ticket | Done when |
|---|---|---|
| E4.1 | Content-addressed object store | Identical content stored once |
| E4.2 | `RefStore` port with a local implementation | Second implementation nameable |
| E4.3 | `Versioner`: status, commit, list_versions, read_version, diff, restore, reconcile, purge | Round trip: commit, mutate, diff, restore |
| E4.4 | Working-copy port over the mutable tree | Reconcile resolves divergence between working state and refs |
| E4.5 | Concurrency proof: a fault-injecting ref store that conflicts once | Conflict path is exercised, not asserted around |

## E5 — Namespacing and isolation

| ID | Ticket | Done when |
|---|---|---|
| E5.1 | Path derivation in one place: workspace and user scope | Single function computes every scoped path |
| E5.2 | **Refuse** multi-tenancy; drop `tenantId` | Recorded. The Forge is one tenant; workspace and user scoping stay |
| E5.3 | Workspace lifecycle: create idempotent, list, delete | Delete removes files, vectors, graph nodes, KV keys and queue entries |
| E5.4 | Cross-user isolation test | A bug in one user's workspace is not observable from another's |

## E6 — Ingestion

| ID | Ticket | Done when |
|---|---|---|
| E6.1 | Resource routes: create, upload, upload-complete, signed download, delete | Large file streams and finalises separately |
| E6.2 | Async pipeline consuming the queue | Ingestion does not block the request |
| E6.3 | Processors as one package per format, starting text and PDF | Adding a format is a package, not a switch edit |
| E6.4 | L0/L1/L2 sidecar generation | Every ingested document has abstract, overview and detail |
| E6.5 | Content escalation routes: abstract, overview, read, stat, download | Retrieval returns L0; caller escalates only where needed |
| E6.6 | Indexing status endpoint | Caller can tell when a document became searchable |
| E6.7 | Indexer dispatch: fan work to handlers | Handler set extensible without editing dispatch |
| E6.8 | Speculative readahead | Prefetch measurably reduces first-read latency, or is removed |

## E7 — Retrieval

| ID | Ticket | Done when |
|---|---|---|
| E7.1 | LLM gateway: one routed path for embeddings, extraction and summarisation | No plugin holds a provider client |
| E7.2 | `EmbeddingProvider` port over Ollama | Swapping provider is config |
| E7.3 | `vectorfs` over pgvector | ANN query returns ranked results through the VFS verbs |
| E7.4 | `EntityExtractor` port; entity and relation extraction | Extraction runs through the gateway |
| E7.5 | `graphfs` — in-memory graph, blob-persisted, single-hop expansion | GraphRAG expansion returns neighbours of a matched entity |
| E7.6 | `graphdbfs` — real graph backend, multi-hop traversal, configurable hop depth | Two-hop Cypher-equivalent query returns results |
| E7.7 | **Open decision:** graph backend choice, constrained to free and laptop-local | Backend named with the second implementation nameable |
| E7.8 | `hybridfs` — Reciprocal Rank Fusion over vector and graph | Fusion needs no score calibration between the two rankers |
| E7.9 | Search routes: find, search, retrieve, glob, grep, scoped by URI prefix | One `find` serves memories, documents and skills by changing the prefix |
| E7.10 | `DocStore` port behind the vector index | Local and object-store implementations both pass |
| E7.11 | Graph persistence and snapshot ports | Graph survives restart; snapshot restores it |

## E8 — Memory

| ID | Ticket | Done when |
|---|---|---|
| E8.1 | Sessions: KV metadata plus append-only message log | Create, list, get, delete, append |
| E8.2 | `context` route: builds prompt context from profile and hybrid retrieval | Returns memories and documents for a turn |
| E8.3 | Commit: extraction of structured facts with priorities | Transcript becomes typed memories |
| E8.4 | Commit: transcript compression | Cold transcript reduced to what is worth keeping |
| E8.5 | Commit: batched category deduplication | A restated fact does not become a second memory |
| E8.6 | Commit: consolidation into narratives | Related facts merge |
| E8.7 | Commit: reconciliation against contradicting prior memories | Contradiction resolved, not duplicated |
| E8.8 | Temporal validity intervals and as-of queries | Reconstructs what was believed at a past moment |
| E8.9 | Memory lifecycle routes: archive, consolidate, restore, temporal graph query | Archived memory is recoverable, not deleted |
| E8.10 | Session archival | Cold transcript moved aside with a summary retained |

## E9 — The resource kernel

| ID | Ticket | Done when |
|---|---|---|
| E9.1 | Skills registry: list, create, get, delete, search, tree, bundle, copy | Semantic discovery returns relevant skills for a task |
| E9.2 | Blueprints on the identical shape | The shape generalises with no new route logic |
| E9.3 | Extract the kernel from what E9.1 and E9.2 actually needed | A third document kind needs config, not code |
| E9.4 | Relations: list, link, unlink | Curated edges distinct from extracted ones |
| E9.5 | Threads: durable agent-run outputs | Results survive the requesting connection |
| E9.6 | Filesystem passthrough routes: ls, stat, tree, mkdir, cp, mv, find, glob, grep, symlink, delete | Agent walks its workspace as a directory tree |
| E9.7 | KV routes: get, put, delete | Namespaced scratch state |

## E10 — Projections and analytics

| ID | Ticket | Done when |
|---|---|---|
| E10.1 | Projection manifest format: slice and rollup | Manifest declares the projection; no schema written by hand |
| E10.2 | DuckDB materialisation to Parquet | Slice Parquet per manifest |
| E10.3 | Rollup across slices | One combined Parquet per workspace |
| E10.4 | Convergence proof | Re-running is a no-op producing the same result |
| E10.5 | Scheduler with write-ahead log | Interrupted job is recoverable |
| E10.6 | Single-consumer guarantee | Two nodes cannot corrupt the same Parquet; crash simulation in tests |
| E10.7 | Natural-language workspace query | NL to SQL over the rollup |

## E11 — Sandboxed execution

In scope. Sandboxing user-authored code is a correctness requirement, not hardening.

| ID | Ticket | Done when |
|---|---|---|
| E11.1 | Mount the scoped VFS as a real filesystem | Agent code sees an ordinary directory tree |
| E11.2 | **Open decision:** Go FUSE library, with the sandbox boundary named | Library chosen; escape surface written down |
| E11.3 | Scope enforcement at the mount | A process inside cannot address a path outside its workspace |
| E11.4 | Escape test suite: path traversal, symlink escape, handle reuse | Each attempt fails and is asserted |
| E11.5 | Lifecycle: mount, run, unmount, cleanup on crash | No orphaned mounts after a killed process |
| E11.6 | **Refuse** 9P and the gVisor gofer wrapper | Recorded. Fleet-scale Kubernetes machinery; revisit on evidence |

## E12 — Operability and toolchain

| ID | Ticket | Done when |
|---|---|---|
| E12.1 | Layered config: defaults, file, environment, flags | Later wins; invalid config fails at boot |
| E12.2 | Effective-config endpoint, secrets redacted | "What is it configured as" never requires guessing |
| E12.3 | Retry with explicit retryable versus fail-fast classification | A validation error is never retried |
| E12.4 | Timeouts on every remote call; deadlines propagate to the driver | No unbounded call |
| E12.5 | Circuit breakers with domain fallbacks | One failing provider degrades a feature, not a path |
| E12.6 | OpenTelemetry traces, metrics and logs | "Why is it slow" answerable without adding code |
| E12.7 | Traffic accounting surfaced through a server-info mount | Per-plugin counters readable |
| E12.8 | One command up, one command down | Returning after three weeks requires no recall |
| E12.9 | **Refuse** the Kubernetes writer/reader split | Recorded. One process until it demonstrably hurts |
| E12.10 | Health and liveness endpoints | Probe answers without touching a dependency |
| E12.11 | Key-space partitioning for contended structures | Contention measurably reduced, or the partitioning is removed |
| E12.12 | Pinned toolchain (Go 1.24.6) and a reproducible build | Same source produces the same binary on a clean checkout |
| E12.13 | `gofmt` gate | Unformatted code fails the build; no formatting opinions anywhere else |
| E12.14 | `golangci-lint` v2 config: `errcheck`, `staticcheck`, `depguard` | Config loads under v2 nesting (`linters: settings:`); E1.6 rules live here |
| E12.15 | Context-discipline linters: no `Context` in a struct, `ctx` first parameter | Tenet 4 violation fails lint. Confirm exact linter names with `golangci-lint help linters` |
| E12.16 | `goleak` in the test suite | A goroutine with no cancel path fails its test. Not statically checkable |
| E12.17 | Timeout coverage on every outbound call | No linter does this. Enforced by review and a test that asserts a deadline reaches the driver |

---

## Sequencing

E1 and E2 first — the `hello` workload from the Forge build order is the right consumer.
E3 and E4 next; ingestion (E6) needs both. E7 needs E6 for something to retrieve. E8 needs
E7. E9 needs E4 for versioning. E10 and E11 are independent of each other and of E8, and
can run whenever their dependencies are met. E12 accrues throughout rather than arriving
at the end, with one exception: E12.14 lands before E1.6, because the architecture
boundary is a `depguard` rule inside that config rather than a standalone script.

## What done means

Per ticket: the capability exists, behind a port whose second implementation is nameable,
with the refusals recorded as decisions rather than omissions.

Surface-level imitation of any other system is not the target. Nerve is Go, with a `.proto`
as its versioned contract and RPC-shaped handlers.
