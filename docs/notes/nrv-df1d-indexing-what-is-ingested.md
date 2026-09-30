# nrv-df1d — Indexing what is ingested

An ingested document's words now reach a vector index. The stage list is
extraction, then `ingest.Dispatch` over one `ingest.TextIndexer` writing the text
sidecar into the corpus of the mount `ingest-index` names.

```
config ingest-index   -> prefix of the index mount, e.g. /vectors
indexers()            -> checks the mount, builds TextIndexer over /vectors/docs
pipeline()            -> Extraction, then NewDispatch(indexers...).Stage()
TextIndexer.Index     -> s.View(tree, /vectors/docs).Write(/<name>, text)
```

## What the pattern was

**The key is derived by the view the reader uses.** The indexer writes through
`Scope.View` over the corpus root, and `recall.Routes` reads the corpus through the
same view. `<user>/<workspace>/<name>` is therefore one derivation used twice, not
two spellings that have to agree. `TestAnIngestedDocumentIsInTheContextOfATurnInItsWorkspace`
checks the contract end to end: it uploads through the resource store, runs the
pipeline the build composes, and reads the document back through `recall.Routes`
over a real vectorfs mount.

**The descriptor is checked at boot.** The indexer does not care which kind of
index stands at the prefix. The composition root checks that the prefix is a
mount and that `<prefix>/docs` is a writable flat key space. A tree refuses a
deep key whose parents do not exist, so a memfs mounted there would boot healthy
and then fail every document at the index stage:

```
ingest-index "/index": /index/docs is not a writable flat key space, and a document
key is <user>/<workspace>/<name>: every document would fail at the index stage
```

Config refuses an index when no queue feeds it. Only the worker writes into the
index, so that build would claim to index and never write anything.

**A document with no words is left alone.** This matches extraction and
summarisation: a format nothing reads leaves no sidecar, and a rerun of the same
job would get the same answer.

## Evidence

Gates, on the branch rebased on main:

```
$ make fmt-check lint
0 issues.
$ make race            # 64 packages ok, rc=0
$ make repro
reproducible: 0f1c37215586f956fea348411b73b5188b145365c8500ad86a18ff39600708e1
```

The live round trip (upload over connect-go to a running nerved, ranked by a
vectorfs query) and `make test-index` need Postgres and RabbitMQ. Neither was up
on the host this was built on. See the bead for the run.

## Deliberately not done

- graphfs indexing (entities and relations). Only vectors are indexed.
- Workspace-scoped ranking. That is nrv-h2iw, and the fused top-N stays shared.
- Backfilling documents ingested before this landed.

## What surprised me

Nothing reads an index from outside the process. The inbound surface takes
documents in and serves only their content back. So "ranked by a vectorfs query"
against a running nerved has to be observed by a second process that mounts the
same index config.

## What I still do not understand

Whether `Handles` will ever be more than `true` for a text indexer. Only reading
the sidecar can tell whether a resource has words, and `Handles` is not given a
context to read with.
