# nrv-5s9 — The audited graph did not survive git

The bead reported one missing pair of edges: E1.4 forwards descriptors and
accessors, so it depends on E1.2 and E1.3, and declared only E1.1. Two polecats
were dispatched 22 seconds apart on that reading and collided in one file.

Both edges are present in Dolt now, and so is the audit the bead asked for — 84
`blocks` edges across E1 through E12, read from each ticket's acceptance text.
Every still-open ticket already carries the edges its acceptance implies:
`e7.8 -> e7.3, e7.5`, `e8.2 -> e7.8`, `e8.9 -> e8.6, e8.8`. Nothing was left to
add there.

What was still wrong was the copy that outlives the database.
`docs/roadmap-plan.json` is the import artifact for the whole roadmap, and it
carried the original 24 edges — including `e1.4 -> e1.1` and nothing else. The
file is a strict subset of Dolt: no edge in it is absent from the graph, so it
was stale rather than contradictory, which is why nothing complained.

`.gitignore` says it plainly: `.beads/  # Issues live in the town Dolt server,
not in git.` The graph is local runtime state. The plan file is the only
versioned record of it, so re-importing on a clean checkout would have restored
the star on E1.1 verbatim, along with the other 59 missing edges. The audit was
real work that lived in exactly one place.

The edges block is now regenerated from the audited graph. Nodes are byte-identical.

## What the pattern was

Two representations of one fact, one of them derived and one of them committed,
with no step that compares them. The derived one was corrected; the committed one
was not, and correcting it is not something the graph itself can prompt — a stale
subset produces no blocked ticket, no failed query and no lint error.

Same shape as the bug it fixes. A missing edge surfaces the first time someone is
blocked by its absence. A missing edge in a file nobody reads until disaster
recovery surfaces during disaster recovery.

## What surprised me

**The audit was already complete where it mattered.** The expectation on picking
this up was a roadmap of unmapped tickets. Instead every open ticket was mapped
correctly and the only defect left was in a file the bead never mentions. Reading
the current state before working from the bead's description was the whole
increment.

## What I would do differently

Check the versioned artifact first when a bug is about state held in a database.
The question "where else is this fact written down" was the fast path here, and
it was the last thing asked rather than the first.

## What I still do not understand

Nothing keeps the two in step. The plan file drifts the moment an edge is added
in Dolt, silently, and the roadmap is close enough to finished that building a
sync check now would cost more than it protects. Filed rather than built.
