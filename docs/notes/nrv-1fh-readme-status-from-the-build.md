# nrv-1fh — A count in prose is a fact with no test

The README's Status section named how many storage kinds the build had. Three
beads were filed against it independently, and the three disagreed: ten, eleven,
fourteen. All three were wrong. The registry holds eighteen.

## How the number was settled

Not by counting `registry.Register` lines, and not by trusting any of the three
filings. The binary already answers the question, because an unregistered kind
is a boot failure that names the ones that are registered:

```
$ go build ./cmd/nerved
$ echo '{"mounts":[{"prefix":"/x","kind":"nosuchkind"}]}' > /tmp/kinds.json
$ NERVE_CONFIG=/tmp/kinds.json ./nerved
nerved: mount "/x": kind "nosuchkind" is not registered; this build has devfs,
exportfs, graphfs, heartbeatfs, hellofs, httpfs, hybridfs, kvfs, localfs, memfs,
objectfs, proxyfs, queuefs, serverinfofs, sqlfs, streamfs, streamrotatefs,
vectorfs: file does not exist
```

That error exists to make a misspelled kind legible at boot. It also happens to
be the only surface that enumerates the registry, so it is the cheapest true
answer to "what does this build have" — cheaper than reading a composition root
that spans a hundred lines of commentary, and it cannot drift from the binary
because it *is* the binary.

## What surprised me

Three agents reading the same file each produced a different count, and none of
them produced the right one. A number in prose has no test behind it, so nothing
fails when it rots — the only signal is a reader noticing, and a reader who
notices files a bead with their own miscount in it. The failure was not that the
number was stale. It was that a number was there at all.

## What the section says instead

No count. The kinds are grouped by what they promise — trees, object stores,
logs and queues, retrieval, composed, synthetic — because that grouping is the
descriptor vocabulary the architecture already uses, and a reader who wants to
know what a mount will do is asking about the promise rather than the tally. A
kind landing joins a row; nothing above it has to be recounted.

The section also says the build is the authority on the list, which is true and
was true before. It was just easier to read the paragraph than to ask.

## What I would do differently

The table still needs an edit when a kind lands, so it can go stale in the small
way rather than the arithmetic way — a missing row rather than a wrong number.
The rot-proof version is no list, only the command; that reads as a README that
will not say what it ships. The trade was taken deliberately: a row a reader can
see is missing beats a number no reader can check.

## What I still do not understand

Whether the operator surface should enumerate the registry. `GET /config`
answers with the mount table — what was mounted — and nothing answers with what
could be. The boot error is a strange place for the only list of a build's
capabilities to live, but adding an endpoint for it is a ticket nobody has
written and a surface nobody has asked for.
