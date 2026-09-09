# nrv-jfh7 — a stability vocabulary that no domain owns

`nrv-kj7` needed a retry loop and a circuit for the model gateway and found
both already written, in `internal/core/vfs`. Reusing them was right — a second
retry loop is a second answer to when a failure is worth another attempt — but
it left `llm` importing the storage package to get one:

```go
// internal/core/llm/registry.go, before
retry   vfs.Retry
breaker vfs.Breaker
circuit *vfs.Circuit
```

Core-to-core and legal under Tenet 1, and still backwards to read.

## The split is between the policy and what it wraps

`internal/core/stability` takes the values and the judgement: `Retry` with
`Do`, `Breaker`, `Circuit` with `Guard`, and `Classify` with `Class`,
`ErrTransient` and `ErrUnavailable`. None of them mentions a path, a mount or a
`FileSystem`.

`vfs` keeps the four decorators — `WithTimeout`, `WithRetry`, `WithBreaker`,
`WithTelemetry` — and every `FileSystem` type behind them. Those are the parts
that know a storage verb: that `Remove` and `Rename` are made once, that a
read-destructive path loses an entry to a second read, that an open is
repeatable on the same terms as the verb it stands in for. That knowledge is
the storage domain's and stays there.

`classify.go` moved whole. `retry.go` and `breaker.go` split along that line:
`Retry.Do`, `delay` and `wait` left; `WithRetry`, `retrying` and the three
`retrying*` accessor types stayed. Same for `Circuit` against `guarded`.

The result is what the ticket wanted:

```
$ grep -rn "core/vfs" internal/core/llm/*.go | grep -v _test.go
$
```

## What surprised me

**Two doc comments were justifying an export that no longer needed justifying.**
`Retry.Do` said "it is exported because the tree is not the only remote thing
this process calls", and `Circuit` said "it is exported for the reason Retry.Do
is". Both were arguing against the package they lived in. In `stability` there
is nothing to argue: the package exists to be called from more than one domain.
Rewriting them was the only non-mechanical part of the move.

**`goimports -local` disagrees with this repo.** It writes three import groups —
stdlib, third-party, module — and the repo writes two. Running it over `internal`
and `cmd` to fix the new import churned five files that have nothing to do with
this ticket and reordered `sqlfs/transient.go`, which deliberately puts the nerve
import above the sqlite ones. Reverted all six by hand. The lesson is that an
import fixer's opinion is a formatting opinion, and this repo's is `gofmt`'s.

**A new package under `internal/core` is not free.** `internal/leak/wiring_test.go`
walks every directory holding test files and fails any without a `TestMain`
calling `leak.VerifyTestMain`. The move produced a green build, a green vet and a
failing suite, from a package I had not written a line of concurrency into. That
is the check working: it is the one gate that a directory added by `git mv` can
fail.

## What I would do differently

Nothing about the shape. The one thing worth saying plainly is that the move was
cheap because the values were already values: `Retry` and `Breaker` carry no
behaviour, and `Circuit` carries only the state one breaker needs. Had either
held a `FileSystem`, the split would have been a redesign rather than a move.

## What I still do not understand

Whether `Timeout` belongs here too. `WithTimeout` is a `FileSystem` decorator
over a `time.Duration`, so there is no policy value to move — but `llm` bounds
its provider calls with a duration of its own, and `config` carries both. If a
third caller wants one, the question is whether a `stability.Timeout` value is
worth having or whether a bare `time.Duration` is already the honest type.

Whether `Throttle` and `Debounce`, when they land, belong in this package or
beside their callers. Tenet 3 names five patterns and this package now holds
three. The argument for one home is the one that just paid off here; the
argument against is that a debounce with no caller is a layer that cannot be run.
