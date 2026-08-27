# nrv-4pm — A key beside a prefix of the same name

`sessions/abc` and `sessions/abc/state` can both exist in an object store. No
tree allows it, so `ReadDir` has to decide what `abc` is, and until now every
object-store backend decided alone. `kvfs` met it first (nrv-rcm) and ruled that
the key wins: a key describes itself, where a prefix is only ever described by
what sits beneath it. The rule was right and lived in one plugin.

The suite now carries it. `Object store lists a key over a prefix of the same
name` is gated on `ObjectStore` and `writable`, stores both, reads both back,
and asks for the listing twenty times over.

## What the pattern was

The rounds are the case. `kvfs` folds a listing through a map, so the answer
follows Go's map iteration order and a wrong rule agrees with the right one
about nine runs in ten. One assertion would have passed against the bug; twenty
leave it nowhere to hide. The count is a named constant so the next reader knows
it is doing work rather than being decoration.

The descriptor's own doc gained the rule, because a plugin author reads `Caps`
rather than the suite: an `ObjectStore` mount now promises that a key and a
prefix can share a name and that a listing answers with the key.

## What surprised me

**The suite found the third backend the moment it was written.** `objectfs`
failed on round zero with `[abc abc]` — not a flake, a duplicate. S3's
`ListObjectsV2` with a delimiter returns `sessions/abc` as an object *and*
`sessions/abc/` as a common prefix, and `ReadDir` appended both without ever
comparing them. The fix is four lines: name every object first, and skip a
prefix whose base name is already taken. MinIO would have listed the same entry
twice, once as a file and once as a directory.

That is the bead's argument, arriving on schedule. Two independent
rediscoveries, and the third backend was already written and green against a
suite that never asked.

**The suite's own object-store stub could not run the new case.** `objectBucket`
in `descriptors_test.go` was `memfs` wearing the descriptor, and a tree cannot
hold a key beside a prefix — `Write("/sessions/abc/state")` failed with `a file
is in the way`. A stub that declares `ObjectStore` and stores a tree can be
asked every object-store question except the only one a tree never faces. It is
now a flat keyspace, which is what `kvfs` already is.

## What I would do differently

Write the collision case before the second backend, not after the third. The
descriptor machinery was built against stubs, and a stub behaves simply by
construction: it declares honestly and then does the easy thing. Every gap found
this way has come from a real backend, which means the suite is behind the
plugins rather than ahead of them.

`kvfs`'s own `TestAKeyBesideAPrefixOfTheSameName` is gone. It asserted exactly
what the suite now asserts, and the plugin runs the suite.

## What I still do not understand

Whether `Rename` across a collision needs a rule too. Renaming `sessions/abc`
moves the key and leaves everything under `sessions/abc/` where it was, which
is right for a keyspace and would astonish anyone reading it as a tree. Nothing
asserts it either way, in the suite or in a plugin.
