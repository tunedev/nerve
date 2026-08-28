# nrv-p9b — Under-declaring was caught by accident

The conformance suite gates on what a plugin declares. Claim nothing and every
case takes the ordinary branch, so the easy way to pass is to lie. That is not
cosmetic: `WithRetry` reads `ReadDestructive` and `AppendOnly` to decide whether
a failed `Read` or `Write` may be made again, so an under-declared mount gets
calls repeated that must not be — an entry lost or bytes stored twice, out of a
green run.

Two ordinary assertions already caught it. `Read copies what it hands back`
reads a second time to prove the returned slice is a copy, and a consuming
backend cannot answer that second read. `Write replaces the whole content`
writes short over long, and an appending backend leaves the tail behind. Both
protections were side effects of asserting something else.

The suite now asks the question on purpose. `descriptorCases` gained two cases
gated on the descriptor being *absent*: a mount that did not claim
read-destructive must answer a second read with the same bytes, and one that did
not claim append-only must let a second write stand in place of the first. Both
name the descriptor in their failure message, and a comment above them says why
they are load-bearing.

## What the pattern was

Descriptor gating had only ever run one way — a case exists because a claim is
set. Running it the other way costs nothing: `writable`, `replacing` and
`repeatable` were already the negative predicates, so the two new cases reuse
`needs(writable, repeatable)` and `needs(writable, replacing)` verbatim. A
plugin that honestly claims the descriptor is not asked, which is the same
mechanism that keeps a read-only mount from being asked to store.

The append case writes `"first"` then `"later"` — same length, deliberately.
`Write replaces the whole content` writes long then short, so it fails on a
leftover tail; this one fails only on accumulation. Neither is the other's copy.

## What surprised me

**The second read had to bypass `mustRead`.** The first draft read through the
helper, and a consuming mount made it `t.Fatalf` with `file does not exist` from
inside `mustRead` — a true failure that says nothing about descriptors. Reading
directly and folding the error into one `t.Errorf` puts `must claim
ReadDestructive` in front of whoever broke it. A helper that fails correctly can
still fail uninformatively.

**Proving the case works meant writing a liar.** A mount that consumes on read
and claims `vfs.Caps{}`, and one that appends on write and claims the same. Both
fail the new case and the incidental one, separately — which is the evidence
that the new case stands alone. The liars were scratch and are not committed:
Go's `testing` has no way to assert a subtest failed, so keeping them would mean
a red suite.

## What I would do differently

Ask what a case protects before writing it, not after. The second read in
`Read copies what it hands back` was doing two jobs and only one was written
down; anyone tightening it to compare against a cached value would have removed
the other silently and the suite would still have looked correct.

## What I still do not understand

The other descriptors are unpoliced and stay that way. `ReadOnly` is
self-enforcing — claim it and the suite demands refusals. `ObjectStore` and
`Broadcast` cannot be checked from outside the port: nothing in the interface
distinguishes a flat keyspace from a tree that never collides, and there is no
second reader identity to tell broadcast from competing. Whether under-declaring
either can lose data the way retry does is not something I established.
