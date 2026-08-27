# nrv-dzx — RemoveAll left a key MinIO would not list

`ListObjectsV2` on MinIO `RELEASE.2025-04-22T22-12-26Z` does not return
`p/c/abc/state` while `p/c/abc` exists, with or without a delimiter. Head and
Get answer for both, and the deeper key lists again the moment the shorter one
is deleted. Real S3 is a flat keyspace and returns both.

`RemoveAll` in `kvfs` and in `objectfs` deleted one listing's worth, so a
`RemoveAll` over a collision left the hidden key in the bucket. Working deletion
is a correctness requirement here, so this was a data-retention defect rather
than a listing curiosity.

Both now delete the key at the name first and then list again until a listing
answers with nothing. Each pass deletes what it found, so a pass that finds
something makes the next one shorter, and the store cannot hide a key from every
pass — it hides one only while a shorter key of the same name is still there.

The suite carries the assertion: `Object store removes a key with a prefix of
the same name under it` stores the colliding pair, removes the shared name and
asks `Stat` for both. `Stat` rather than a listing, because the listing is the
thing that lies.

## What the pattern was

Re-listing looks like it cannot work. A store that hides a key should hide it
from every listing, and the loop should spin or terminate early with the key
still there. It works because the hiding is conditional: the shorter key is what
hides the longer one, and deleting it is the first thing `RemoveAll` does.

It is also the honest reading of the verb against any store. "Everything under
this path" is not satisfied by one listing's worth for reasons that have nothing
to do with MinIO — a paginated listing and a concurrent writer both leave more
behind.

The fix is written twice rather than once. `kvfs` reaches a `Store` and
`objectfs` reaches a `Bucket`; the two ports have no common type and share
nothing but the rule. What they share is now in the suite, which is where a rule
belongs when the code cannot hold it.

## What surprised me

**The case added to close nrv-4pm passes against MinIO for the wrong reason.**
It asserts that a listing answers with the key. MinIO answers with the key
because it never offers the prefix. Same observable, different mechanism, and
the fold rule that case exists to pin is never reached on that backend. A green
conformance run was evidence for `kvfs` and `objectfs` over a map, and a
coincidence over MinIO. The case's doc says so now.

**`objectfs` failed the new case on both names, not one.** `Stat` of the shared
name answered `nil` after the `RemoveAll`, because the surviving deeper key
still makes the prefix exist and the mount reported a directory. One leftover
object, two wrong answers.

## What I would do differently

Ask a store what it has rather than what it lists, whenever the assertion is
about what survives. The listing is convenient and it is the one thing under
suspicion in any case about prefixes.

## What I still do not understand

Whether any other verb reads a listing as though it were complete. `ReadDir`
returns what the store offered, which is the store's answer to give. `Rename`
across a collision moves the key alone, which nrv-4pm already left open.
