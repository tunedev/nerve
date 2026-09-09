# nrv-1b0 — the exclusion belongs to the thing being swept

`Versioner.commits` was a `sync.RWMutex` field on `Versioner`. `Commit` took it
for reading, `Purge` took it for writing, and the comment said the exclusion
"holds for this process and for no other".

It did not hold for a second versioner in this process either. Two versioners
over one `cas.Store` — what `working_test.go` and `conflict_test.go` already
build, and what a second working copy is — hold two different mutexes and
exclude nothing. A `Purge` on one lands in the window the other opened between
`objects.Put` and the ref that names what it put, and deletes it.

The exclusion now lives on `cas.Store` as `Retain` and `Sweep`, two scoped
callbacks. `Commit` runs its body inside `objects.Retain`; `Purge` runs its body
inside `objects.Sweep`. Two versioners over one store take one lock because they
hold one store.

## What the pattern was

The rule that decides it: **the exclusion belongs to the thing being excluded
from, not to the caller doing the excluding.** A sweep empties the object store,
so the object store is where a sweep is held off. A lock on the caller can only
ever exclude that caller, and the whole point of the invariant is the caller you
did not know about.

Scoped callbacks rather than exported `Lock`/`Unlock` because they cannot be
half-taken, and because a caller holding a `*cas.Store` then cannot fail to be
inside the exclusion — there is no second way to reach `Put` and `Remove` in a
window. `Retain` and `Sweep` are named for what the caller is doing, not for
which half of an `RWMutex` they take: the store keeps deciding nothing about
what is reachable, which stays the versioner's.

## The surprise: the fix that makes the test deadlock

The obvious test drives the sweep synchronously inside the commit's window, the
way `conflictOnce` drives the other writer inside the ref race. Against the
fixed code that is a deadlock, not a pass — the commit holds the read lock and
the sweep wants the write lock on the same goroutine.

So `sweepDuring` starts the sweep in a goroutine and waits on it with a bound.
The bound is not a timing assumption, which is what made it acceptable: the
sweep either finishes inside the window, which is the exclusion missing, or it
is blocked on an exclusion that cannot release until the commit returns, so
waiting longer proves nothing new. With `Retain`/`Sweep` reduced to plain calls
the test fails on the real symptom — `ReadVersion` of the version the commit
just returned reads an object that is gone.

## What is still open

`Purge`'s roots are every ref plus **this** versioner's working-copy base. A
second versioner's base is not a root, so deleting the ref a second copy stands
on and sweeping from the first strands it — the single-copy version of which is
`TestPurgeKeepsTheVersionTheCopyStandsOn`. Same shape as this bug: a sweep
scoped to one versioner over a store that is not. Filed as `nrv-1qof`; closing
it wants a way for a copy to be named from outside the versioner holding it, not
a longer roots list.
