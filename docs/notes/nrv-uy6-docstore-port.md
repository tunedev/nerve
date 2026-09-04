# nrv-uy6 — the DocStore behind the vector index

`vectorfs.Index` held documents and vectors in one Postgres row. It now holds
vectors alone, and the documents are behind `vectorfs.DocStore`: a directory on
the local disk, or a corpus in an object store. Both run the same cases, so a
`vectorfs` mount means the same thing over either — a bucket full of documents
with a database ranking them, or both on a laptop.

```
/docs/<key>    the corpus. The DocStore keeps it; writing embeds, reading gives it back
/query/<text>  the question. The Index ranks; reading answers
/config        what the index is
```

```
$ go test -count=1 -v ./internal/adapters/outbound/vectorfs/ | grep -c -- '--- PASS'
133                     # 21 port cases per corpus, then the mount suite per corpus

$ go test -count=1 -v -run TestDocStorePort ./internal/adapters/outbound/vectorfs/ \
    | grep -E '^    --- PASS'
    --- PASS: TestDocStorePort/LocalDocs (0.00s)
    --- PASS: TestDocStorePort/ObjectDocs (0.00s)

$ make check
go tool golangci-lint run
0 issues.
reproducible: cb1ac57771b614694f129c029218372d8e5e4aa3e5a94587ad3fe18f221aafec
```

`make test-store` and `make test-index` were not run here: this worktree cannot
reach Docker (`make status` = `docker: Permission denied`), so the MinIO run of
the port cases and the pgvector run of the mount suite are unexecuted. Both are
wired: `test-store` now includes this package, and `test-index` already did.

## What the pattern was

**kvfs was the template, verb for verb.** `DocStore` is `kvfs.Store` with a
`DeleteAll` and a `Chmod`, `ObjectDocs` is `kvfs.ObjectStore` over the same
`objectfs.Bucket`, and the cases are one set run per implementation. Nothing
here was invented; the second implementation of a flat keyspace had already been
written once, and reading it was faster than designing it again.

**The corpus is the record and the ranking is derived.** That single sentence
decides the order of every verb that touches both: store then index, unindex
then unstore. Both orders leave the same recoverable state — a document that is
there and not yet findable — and the opposite orders leave an answer naming a
document nobody can read. Writing the document again repairs it, which is a
thing an indexer already knows how to do.

**A key is a name, so the local corpus files it where a directory cannot go.**
A corpus holds `notes` and `notes/one` at once and a directory tree does not, so
every document is filed under its key plus `.doc`: `notes.doc` sits beside the
directory `notes/`. Without it a corpus on a disk would quietly mean less than
one in a bucket while the mount claimed the same object-store descriptor over
both — the mount conformance suite is what makes that concrete, and it now runs
once per corpus rather than once.

**The table converges rather than being migrated.** `vector_<name>` used to
carry `body`, `mode` and `written_at`. The schema statement drops those if they
are there, deletes the rows with no vector, and sets the column not-null — so
running it against a database from before E7.10 leaves exactly what a fresh
create would have made, and running it twice is a no-op that proves the first.

## What surprised me

**The atomicity E7.3 celebrated was the thing this ticket had to spend.** That
note's best line was that one row and one statement make "in the corpus" and
"findable" the same fact. Two stores cannot keep that, and no amount of ordering
buys it back. What ordering does buy is that every failure lands on the same
side — stored but not yet findable — and that side is repairable by a retry of
the write. A rename is the exception and it has no safe order: whichever store
moves first, a failure before the second leaves the ranking naming a key the
corpus does not have. Nothing compensates for it, because a compensating move
fails in the same place.

**Splitting the port made it smaller, not larger.** `Index` lost six verbs and
gained nothing. What is left — file a vector, drop one, drop a prefix, move one,
rank — is the whole of what a similarity index is, and `Drop` of a key with no
vector stopped being an error the moment the store became the thing that says
what exists. The pgvector side lost the `body`/`mode`/`written_at` columns, the
`Peek`/`Get`/`List` statements, the null-vector row and the `WHERE embedding IS
NOT NULL` the null needed.

**A listing carries less than a Head, and only over a bucket.** An object
store's list call reports no metadata, so a mode a `Chmod` set is invisible to
`List` and visible to `Head`; on a disk both agree. kvfs already lived with
this and did not say so in its port; here it is written into `DocStore.List`,
because a caller reading a listing's mode over one corpus and not the other
would have no way to find out why.

## What I would do differently

Ship an in-memory `DocStore`. The offline cases run over `LocalDocs` in a
`t.TempDir()`, which is real and slower than it needs to be, and the mount
conformance now writes a few hundred files per run. A map behind the same verbs
would make the offline suite a fake over a fake, so it was not obviously right
either — but a corpus in memory is also what a `nerved` with no `doc-store-url`
could mount instead of refusing, the way an empty `kv-store-url` mounts a map.
That is the closer parallel to kvfs and it was not taken here: an index that
answers questions about documents it will lose on restart seemed worse than a
mount that refuses to boot. Filed as the open question rather than guessed at.

## What I still do not understand

**Whether the two stores should be one setting or two.** `doc-store-url` is one
corpus for the process, scoped per mount by the index's name, because that is
what `kv-store-url` does. But `index_url` is per mount, so the ranking and the
documents of one mount are configured at different altitudes, and an operator
moving one index's documents to a bucket has to move every index's. Two mounts
that genuinely want different corpora cannot say so today.

**What `hybridfs` reads.** E7.3 left this open and E7.10 did not settle it. The
answer is still a TSV out of a `Read`, and `Index.Nearest` is still most of what
RRF would want. What did change is that a hit is now a key into a store both
rankers could share, which makes a fused ranking over one corpus more obviously
a thing rather than a coincidence.

**Whether a rename should be allowed at all.** It is the one verb with no safe
order across the two stores, and its whole justification is not paying a model
call to move a document. A mount that refused `Rename` under the corpus would
lose that and gain an invariant that holds through any crash. The conformance
suite asks for `Rename`, so refusing it is a descriptor question and not a
plugin one, which is why it was not decided here.
