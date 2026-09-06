# nrv-2ov — an interval nobody wrote down

A memory now carries the interval it was believed over. `Supersede` writes the
memories that replace named earlier ones, `AsOf` answers with what a workspace
believed at a moment, and `Current` answers with what nothing has replaced.

Nothing about the interval is stored. A memory records what it supersedes; the
end of the superseded memory's interval is read off the memory that replaced it.

```
$ go test -count=1 -v ./internal/core/memory/ | grep -c -- '--- PASS'
65

$ make check
...
reproducible: e549403bfb3c0e8f6fdef732ec31972130f8a28780320151cac99fd4da40c2e4
```

The two claims that matter were each broken on purpose and the suite caught both.
Ending an interval at the last replacement rather than the first:

```
--- FAIL: TestTheIntervalEndsAtTheFirstReplacement/tree
    the replaced memory ran until 2026-09-03 09:00:02 +0000 UTC, want 2026-09-03 09:00:01 +0000 UTC
```

Closing the interval at both ends instead of half-open:

```
--- FAIL: TestTheReplacementIsBelievedTheInstantTheReplacedOneIsNot/tree
    AsOf(2026-09-03 09:00:01 +0000 UTC) = [lives in Dublin lives in Berlin], want [lives in Berlin]
```

## What the pattern was

**The append-only rule had already decided the design.** The obvious shape for a
validity interval is two fields on the memory, and there is nowhere to put the
second one: setting an end means writing to a memory after it was written, and
the whole reason the log is append-only is that something derived from a memory
is derived from the memory that was there. So the successor carries the
supersession and the reader derives the end. The ticket looked like a schema
question and was a lifecycle one.

**Derived beats stored when the value is about a relationship.** `Until` is not a
property of the memory; it is a property of the pair. Storing it on one of the
two would have made every writer of the second responsible for going back to the
first, which is the write nothing here is allowed to do.

**`Current` reads no clock.** A memory is current because nothing replaced it,
which is structural, and asking `AsOf(caller's now)` would answer differently on
a caller whose clock is behind the store's. Half of what looks like a temporal
question is not one.

## What surprised me

**Half-open needed saying out loud.** At the instant a replacement is written,
exactly one of the two is believed, and either answer type-checks. The test that
asks at the replacement stamp is the only thing that pins it, and without it the
two implementations differ by a nanosecond nobody would notice until an as-of
query landed exactly on a commit.

**The refusals came out of the same rule as the feature.** Superseding with no
facts reads as a retraction and would write nothing, so nothing would record
that anything stopped being believed — the call would silently do nothing.
Refusing it is not validation for its own sake; it is the log's rule restated.

## What I would do differently

`List` builds a sequence-to-position map and walks every supersession on each
call. For a workspace with a few hundred memories that is nothing against the
read per memory it already does. For one with a hundred thousand, as-of wants an
index — and an index is a derived file that can go stale against an append-only
log, which is a worse problem than the scan it saves. I would want a real
workspace to be slow first.

## What I still do not understand

Whether validity should be bitemporal. What is here is belief time: when the
workspace held the fact. It is not the time the fact was true in the world — "I
lived in Dublin until March", said in June, is one memory believed from June
onwards about a period that ended in March. Answering that needs a second axis
and a caller that has one to give. Nothing asks yet, and inventing the axis now
would put a field on `Fact` that no extractor fills.

## Deliberately not done

**A fact cannot be retracted without a replacement.** `Supersede` refuses an
empty batch. A fact that simply stopped being true, with nothing to put in its
place, has no way to be recorded: it wants a log entry that carries no fact,
which is a second shape in a log whose every entry is a memory. Nothing needs it
yet, and E8.9's archive routes are the likelier home for it.

**Nothing calls `Supersede`.** `Commit` still adds every extracted fact and
compares it against nothing — E8.5 and E8.7 are the callers, and reconciliation
is what turns a contradicting turn into a supersession rather than a second
memory. This ticket is the mechanism those need, wired to no one.

**No route.** E8.9 owns the temporal query route; this is the core capability it
will call.

**A supersession naming a memory that is not in the log fails the whole
listing.** That is the same stance as a memory that will not decode: a short
answer is indistinguishable from a correct one, and here it would read a
replaced fact as one the workspace still believes. It also means anything that
removes a memory file breaks every log referring to it, which is a constraint on
E8.9's archive rather than a bug here — but it is a constraint, and it was
chosen rather than discovered.
