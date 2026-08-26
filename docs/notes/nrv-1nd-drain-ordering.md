# nrv-1nd — the drain ordering, asserted from outside

`cmd/nerved/serve_test.go` drives the real `serve` over a real socket, probes
`/readyz` and `/livez` the way a balancer and an orchestrator do, and asserts
three things about what they saw: readiness failed while the listener was still
open, at least `drainDelay` passed before shutdown was entered, and liveness
kept answering across that window.

## What the pattern was

The ordering in `serve` is three statements with no return value between them,
so there is nothing to assert on directly. What is observable is the sequence
of probe answers and the moments they arrived, so the test records
`(path, code, time)` per probe and asserts order and spacing over that log.
`http.Server.RegisterOnShutdown` supplies the one event that has no answer to
probe for: its hooks fire the moment `Shutdown` is entered.

Timestamps are taken **before** each request, not after. A probe that answers
`ready` proves the process was not draining at some point during the call, and
dating it from when it was asked is the reading that cannot land after the
state it reports changed. The spacing assertion is therefore measured from the
last `ready` answer rather than the first failing one — the last `ready` is at
or before the drain, so the gap it measures is never shorter than the real one,
and the assertion has no slack constant in it.

## What surprised me

**The test is the deliverable, not the wiring — and it has to be shown to
fail.** Four mutations of `serve`, each reverted:

| mutation | caught by |
|---|---|
| `Shutdown` before `Drain` | readiness never failed over an open listener |
| `time.Sleep(drainDelay)` deleted | same — the socket closed before any probe landed |
| `drainDelay / 10` | shutdown entered 25.9ms after the last ready answer, want 200ms |
| `/livez` failing while draining | liveness answered 503 while draining |

Deleting the sleep does not fail the spacing assertion. It closes the listener
within microseconds of the drain, so no probe ever observes a 503 and the
ordering assertion fires first. Both assertions earn their place: one covers
removal, the other covers shortening.

**`serve` takes an `*http.Server` and calls `ListenAndServe` on it, so a test
cannot hand it a listener.** It gets an address instead, reserved by listening
on `127.0.0.1:0` and closing. That is a race in principle and the only seam
this shape leaves.

**The watcher has to be seen working before the drain begins.** The first
version cancelled the context as soon as the probe goroutine was started and
recorded nothing but failures — the drain won. The fix is to block until the
watcher has one `ready` answer of its own, which also makes the value the
spacing is measured from real rather than zero.

## What I would do differently

Nothing in the test. In `serve`, `time.Sleep` is the part that cannot be
driven: a run costs the full `drainDelay` in wall time, and the suite pays
200ms for it. A `func(time.Duration)` parameter would remove that, at the cost
of a parameter that exists only for tests. Not worth it at one call site.

## What I still do not understand

Whether the drain wait belongs in `serve` at all, or whether the listener
should stay open by a mechanism that does not block the goroutine — the
sleep is what makes the refusal observable, and it is also what makes this
test slow. Nothing else needs it yet.
