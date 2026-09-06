# nrv-tv9 — graph persistence and snapshot ports

The graph was already blob-persisted, so surviving a restart was E7.5's. What
E7.11 adds is the second port beside it and the two places that reach it: a
snapshot is taken by writing a label, and the graph is set back to one by
writing that label to `/restore`.

```
/snapshots            the labels
/snapshots/<label>    write takes the graph as it stands, the body its note;
                      read describes it; remove forgets it
/restore              write a label to set the graph back to it; read gives the
                      label the graph stands at, empty once anything has been
                      written since
```

```
$ go test -count=1 -v ./internal/adapters/outbound/graphfs/ | grep -c '^--- PASS'
37

$ go test -count=1 -v -run 'Snapshot|Restore|Label' \
    ./internal/adapters/outbound/graphfs/ ./cmd/nerved/ | grep '^--- PASS'
--- PASS: TestASnapshotTakenBeforeARestartIsWhatARestoreGivesBack
--- PASS: TestARestoredGraphIsWhatOpensAgain
--- PASS: TestTheRestorePathHoldsALabelOnlyWhileNothingHasBeenWrittenSince
--- PASS: TestASnapshotDescribesItself
--- PASS: TestTheSnapshotsList
--- PASS: TestASnapshotIsForgottenByRemovingIt
--- PASS: TestRemoveAllForgetsEverySnapshot
--- PASS: TestALabelIsOneName
--- PASS: TestRestoringALabelNothingWasKeptUnderChangesNothing
--- PASS: TestARestoreTheStoreRefusedIsARestoreTheGraphDidNotMake
--- PASS: TestASnapshotTakenByOneMountIsRestoredByALaterOne
--- PASS: TestASnapshotIsFiledApartFromTheGraph

$ make fmt-check && go vet ./... && go test -race -count=1 ./... && make lint
0 issues.
```

The acceptance discriminates. With the restore's assignment of the snapshot's
documents removed,

```
--- FAIL: TestASnapshotTakenBeforeARestartIsWhatARestoreGivesBack (0.00s)
    snapshot_test.go:38: Read(/neighbours/Nerve) = "...Nerve uses Neo4j 1", want the claim the snapshot held
    snapshot_test.go:44: Read(/docs/stack) = "Nerve uses Neo4j", want the text as the snapshot held it
    snapshot_test.go:47: Read(/docs/crew) = <nil>, want a text the snapshot never had to be gone
--- FAIL: TestARestoredGraphIsWhatOpensAgain (0.00s)
```

## What the pattern was

**Two ports, because their second implementations are two different things.**
The calibration rule asks for the alternative implementation to be nameable, and
the one that matters here is E7.6's graph backend: it snapshots itself — a dump,
a clone, a backup the server takes — and implements nothing of `Store`'s
load-and-save-the-whole-graph. That is what makes `SnapshotStore` a port rather
than four more verbs on the one that exists. One type answers both at the
composition root, which is not a contradiction: a laptop's corpus is where both
go, and the ports are separate because the alternatives are, not because the
laptop needs two places.

**Restore is a write, so it is the same commit every other change is.** It goes
through `save`, which builds the next graph, hands it to the store and only then
makes it the graph the mount answers from. A restore the store refused is a
restore that did not happen in memory either — asserted by the case that sets
the store refusing and reads the mount back.

**Clearing the standing label is by construction rather than by remembering.**
`save` builds the next graph with the documents copied and nothing else carried,
so a change that does not set the label does not have one, and `commit` — every
other verb's entry point — cannot forget to clear it. `/restore` therefore reads
as an exact claim: this graph is that snapshot, with nothing written since. The
alternative, a field cleared at each call site, would have been five places to
get right and one to get wrong.

**A snapshot's path describes it rather than handing over the blob.** A snapshot
is read to decide whether to go back to it, and the way back is the restore
path. Reading the serialised graph out of `/snapshots/<label>` would have made
the mount's own persisted form part of what a caller reads, which is the thing
the `Store` port exists to keep private.

## What surprised me

**The tree had a spelling for taking a snapshot and none for restoring one.**
Writing `/snapshots/<label>` is unambiguous — a write makes the thing named
exist, which is what `/docs/<key>` already does. Going back is the opposite
shape: it is one action with one argument and no name of its own. Rename onto
the corpus was the tempting spelling and it is wrong, because rename consumes
what it moves and restoring should leave the snapshot where it is. A control
path is what a filesystem-shaped platform has for an action, and `/restore`
reads honestly in both directions: the label goes in, and what the graph stands
at comes back.

**The note is the body, which removed the last wart.** A write whose content is
ignored is a wart, and the first draft had one. Making the body the snapshot's
note — the same thing `version.Versioner.Commit` takes a message for — means
`Write` carries something and `Create`, which writes nothing, takes a snapshot
with no note. Neither is a special case.

**The descriptor now varies three ways inside one mount.** The texts are an
object store, the snapshots and the restore path are an ordinary read-write
tree, and the config and expansions are read-only. E7.5's note said no type
could have said two of those; the third arrived without touching the interface.

## What I chose not to build

No snapshot of a snapshot and no history. A label is a name somebody chose, not
a version in a chain, and nothing here records which snapshot a graph was
written from. E4's versioner is the machinery for that, over the tree rather
than inside one mount.

No relabel. `Rename` refuses under the snapshots because moving one would want a
`Move` on the port, and nothing has asked to rename a snapshot.

No pruning and no bound on how many labels a graph carries. Every snapshot is a
whole copy of the graph, so a corpus of them costs what the graph costs times
however many an operator took. That is the operator's to watch, and
`RemoveAll("/snapshots")` is the tool.

## What I still do not understand

**Whether a snapshot should carry the extractions at all.** It carries whole
documents, texts included, so a snapshot of a large graph is a second copy of
every text in it. Keeping only the extractions would be much smaller and would
lose the property that makes the texts an ordinary place in the tree. The number
where the copy stops being acceptable is unmeasured, and it is the same number
E7.5 left unmeasured for the graph itself.

**Whether the standing label survives contact with two writers.** It says the
graph is a snapshot with nothing written since, which is true of this process
and of the record it wrote. Two processes over one corpus already clobber each
other's whole-blob writes — E7.5 said so — and this adds a field that reads as a
claim rather than as data, which is a sharper way to be wrong about it.
