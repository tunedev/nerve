# nrv-9hz — pinning the linter in the repo, not on the PATH

## The pattern

`golangci-lint` is a `go.mod` tool dependency:

```
tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint
```

`make lint` invokes it as `go tool golangci-lint`, and CI runs `make lint` rather than
`golangci-lint-action`. The version lives in one place, in a versioned file, and is the
same one everywhere.

## What surprised me

**A linter that cannot read its config exits 0.** That is the whole reason this ticket
existed. `golangci-lint` v1.64.8 cannot parse `version: "2"`, and even v2.3.0 refuses a
module targeting a Go newer than the one it was built with:

```
can't load config: the Go language version (go1.24) used to build golangci-lint
is lower than the targeted Go version (1.27)
```

Either way the run ends without linting anything, and a gate that lints nothing is
indistinguishable from a gate that passes. CI had been pinned to v2.13.1 and was
genuinely enforcing; `make lint` ran whatever was on the PATH. Local and CI silently
disagreed about whether the tree was clean, and local was the one that was wrong.

**`go tool` also removes the second half of the mismatch.** The binary is built from
source with this module's toolchain, so the "built with go1.24, targets 1.27" failure
cannot recur no matter what Go version a machine happens to carry.

Evidence, on a clean tree:

```
$ make lint
go tool golangci-lint config verify
go tool golangci-lint run
0 issues.
$ go tool golangci-lint --version
golangci-lint has version 2.13.1 built with go1.27.0
```

And under a probe — an `_ "net/http"` import added to `internal/core/vfs/accessors.go`,
which the depguard `core` rule forbids:

```
internal/core/vfs/accessors.go:4:2: import 'net/http' is not allowed from list 'core':
core owns ports; transport belongs in internal/adapters/inbound (depguard)
make: *** [Makefile:70: lint] Error 1
```

The rule fires through the new invocation. The probe was removed afterwards.

## The cost, stated rather than hidden

The tool's dependency graph lands in `go.mod` and `go.sum` — 216 indirect requirements
and roughly 900 lines of sums. That is the price of the version being versioned. The
alternative, a separate `tools/go.mod`, keeps the main module small but adds a second
module to keep in step, and this repo has exactly one tool.

`go get -tool` writes a minimal `go.sum`; `go mod tidy` then adds the tool's test
dependencies. Both are committed tidied, so `go mod tidy` is a no-op on a fresh checkout.

## What I would do differently

Pin the tool at the same moment the config gains a version the old binary cannot read.
E12.14 shipped a `version: "2"` config and left the binary to each machine, which is the
gap this ticket is. A config format change is a toolchain change.

## What I still do not understand

Whether `go tool` costs enough in CI to matter. `setup-go` caches the module and build
caches, so the linter should build once per toolchain change, but a cold CI run pays a
compile that `golangci-lint-action` would have downloaded prebuilt. Worth measuring
before anyone reintroduces the action.
