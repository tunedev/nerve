# nrv-tju — The race detector in the gate

`make test` runs the suite fast with no detector. `make race` runs it under
`-race`. `make check` depends on `race`, not `test`, and CI's test step is
`go test -race ./...`.

## What the pattern was

The gate had two definitions — the `check` target and the steps in
`.github/workflows/ci.yml` — and neither one instrumented for races. E12.11 had
just made `MountTable` lock-free on the read side: readers load an
`atomic.Pointer[[]mount]` and take no lock, writers serialise on a mutex and
replace the slice wholesale. That is the shape where a later edit introduces a
race the ordinary suite passes straight through, because the failure is
nondeterministic and load-dependent.

Splitting `test` from `race` rather than putting `-race` on the one target
keeps the inner loop fast and makes `check` mean what its help text says —
"everything CI runs". `check` does not also depend on `test`: `race` runs the
same tests, so listing both would run the suite twice for nothing.

## What surprised me

**The detector costs almost nothing here.** 0.84s for `go test ./...` against
2.25s for `go test -race ./...` — 2.7x on a ratio, 1.4 seconds in wall clock.
The split is not paying for itself yet. It earns its place the first time a
test sleeps or hammers a mount table, and until then it is an inner loop that
stays instant for free.

**A gate is vacuous until something proves it fires**, the same lesson E12.16
recorded for goleak. There is no race equivalent of `goleak.TestingT` — a
permanent test cannot contain a real race without failing the gate it proves —
so the proof is a throwaway. Two goroutines incrementing an `int`, then:

```
$ make test
ok  	github.com/tunedev/nerve/internal/core/vfs	0.058s

$ make check
WARNING: DATA RACE
Read at 0x00c0000183a8 by goroutine 10:
  github.com/tunedev/nerve/internal/core/vfs.TestProbeRace.func1()
      internal/core/vfs/probe_race_test.go:13 +0x7b
--- FAIL: TestProbeRace (0.00s)
    testing.go:1865: race detected during execution of test
make: *** [Makefile:53: race] Error 1
```

That `ok` from `make test` is the honest cost of the split stated out loud: the
fast target passes a real race, and the gate is the only thing that catches it.

## What I would do differently

Fix the duplication rather than the symptom. The bug was not "`-race` is
missing", it was "the gate is written down twice and the copies drifted" — and
the fix patches both copies, so they can drift again. CI could run `make check`
and hold one definition, but that means installing golangci-lint in the
workflow by hand instead of using `golangci-lint-action`, which pins and caches
it. Worth revisiting with nrv-ejl (E12.12), which is already deciding what the
gate is made of.

## What I still do not understand

Whether the detector will find anything without load. It reports races it
observes, not races that are possible, so a suite whose concurrent tests finish
in microseconds may never interleave the two goroutines that would collide.
`mount_contention_test.go` is the only test that deliberately contends;
`-count` or a `-race`-only stress target may turn out to be what actually
guards the lock-free read path, and this change alone is not evidence that it
is guarded.
