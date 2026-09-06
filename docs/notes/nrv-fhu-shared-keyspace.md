# nrv-fhu — The flat keyspace, stated once

`relative`, `key`, `prefix` and `fold` were copied verbatim into `kvfs`,
`streamfs` and `streamrotatefs`. The duplication between the first two was
deliberate and recorded in `e3.2-streamfs.md`; the third copy is the second case
arriving, so the four functions now live in
`internal/adapters/outbound/keyspace` and the three plugins call them.

They are the whole of what `ObjectStore` means in practice — a name is a prefix,
a key and a prefix can share a name, and a listing of that shared name answers
with the key. The rule is asserted once in the conformance suite (nrv-4pm); it
is now stated once as well.

## What the pattern was

Not a port. There is no second implementation of a lexical rule, so `keyspace`
is a package of functions rather than an interface, and the calibration rule in
the tenets says to call the concrete thing. Each plugin keeps its own map and
its own locking, which is the part that actually differs: `streamfs` locks per
mount and reads streams outside it, `streamrotatefs` holds one lock across a
write because rotation adds and removes names, and `kvfs` holds none because the
store owns its own concurrency.

`kvfs`'s `key` and `prefix` stay methods, because a `kvfs` key is its mount's
namespace and the keyspace key under it. They are two lines each now: the
namespace is the mount's business, the lexical rule is the keyspace's.

## What surprised me

**`dirMode` left with `fold`.** All three plugins declared `dirMode` beside
`fileMode` as if they were a pair, and all three used `dirMode` in exactly one
place — inside `fold`, describing a prefix a listing had synthesized. It was
never a property of the plugin at all. The constant moved into `keyspace` as
`DirMode` and no plugin kept a copy; `fileMode` stayed, because what a stream, a
segment and a value are described with really is each plugin's own.

**The three copies had already drifted in their types.** `kvfs`'s `fold` took a
`kvfs.Entry` and built the `vfs.FileInfo` inside; the other two took a
`vfs.FileInfo` and set its `Name`. Verbatim was true of the rule and not of the
signature. `keyspace.Fold` takes the port's type, so `kvfs`'s `ReadDir` builds
the info at the call site — which is where the store's `Entry` stops being a
store thing and starts being an answer.

**The error message lost its noun.** Three copies said "the root is a prefix and
not a stream", "not a log", "not a key". One function cannot say all three, and
it says "not an entry". The verb and the path are already in the message the
caller sees, and the mount kind never was.

## What I would do differently

Write the mutation check into the test before believing the test. My first
version of `TestAKeyIsListedOverAPrefixOfTheSameName` gave the key and the
deeper entry the same `ModTime`, and it passed with the key-over-prefix guard
deleted: with equal times the prefix's `After` comparison is false, so the key
survived by accident rather than by rule. Making the key the *older* of the two
is what puts the guard under load. The suite's version of this case (nrv-4pm)
defends itself differently, by asking twenty times so map order cannot hide a
wrong answer — two ways of making a listing assertion mean something, and
neither is obvious from reading the assertion.

## What I still do not understand

Whether the tail of `ReadDir` belongs here too. All three plugins still end with
the same three lines — collect the map's values, sort by name, return — and
that is as much "what a listing answers" as the fold is. It stayed out because
the bead named four functions and a fifth is scope I did not ask for; but the
next mount of this shape will copy it, which is the same argument that produced
this package.

Whether `kvfs`'s namespace is a keyspace idea. It is a prefix every key of a
mount sits under, which is the definition `Prefix` already works with, and
`namespaceKey` cleans a name with the same lexical rules. Only one plugin has
one, so it stayed where it is.
