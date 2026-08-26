# nrv-cbe — A kind declares which of its settings can be read back

`GET /config` redacted a credential only where it sat inside a URL. Mount
settings are an arbitrary `map[string]string`, so a key named `secret_access_key`
or `password` was served in full. Redaction now works from a declaration: a kind
names the setting keys it is safe to read back, and every other key of a mount of
that kind reads back as `xxxxx`.

## What the pattern was

The declaration lives where a kind is registered:

```go
registry.Register("s3", s3.New, "endpoint", "bucket")
```

`Registry.Public(kind)` reports what was declared, and `Config.Effective` takes a
`PublicSettings func(kind string) []string` — a one-function port defined at the
consumer, so `config` does not import the registry to ask. The composition root
passes `registry.Public` and is the only place that joins them.

Redaction stayed in `Config.Effective`, and `admin.Handler` still takes the
`Effective` value rather than the `Config`. That structure was not the defect.
Only the predicate was.

## What surprised me

**"One rule beat a list" was true and still wrong.** The previous increment
rejected a per-field `secret bool` because a list drifts, and replaced it with a
rule about the value: redact the password in anything that parses as a URL. The
reasoning was sound for typed settings, where every field is known here. It does
not survive a map whose keys an operator writes, because no rule about a value
can tell a bucket name from an access key — `AKIAIOSFODNN7EXAMPLE` is a
well-formed bucket name. The list came back, but it moved: not a list kept here
of everything secret, a list kept by each kind of the little that is not.

**The failing direction is the whole design.** A deny-list of suspicious key
names fails open — the one key nobody thought of is served. An allow-list fails
closed — a kind that declares nothing echoes nothing, and an unregistered kind
echoes nothing. `Public` returning a nil slice for an unknown kind is not an
edge case to handle; it is the default answer being the safe one.

**The old test read as if it covered the category.**
`TestEffectiveRedactsMountSettings` used an S3 endpoint with the secret inside
the URL, which is the one shape the old rule handled. It tested the covered case
under a name that claimed the general one. A real S3 mount carries
`access_key_id` and `secret_access_key` as separate settings. The replacement
tests name what they actually prove: one that a declared setting still gets its
URL password scrubbed, one that undeclared keys are not echoed, one that nothing
is echoed when nothing is declared.

**Redacted keys are still named.** The value is replaced, the key is not
dropped. An operator needs to see that a mount carries a `secret_access_key` at
all; hiding the key would answer a different question from the one asked.

## What I would do differently

Declare the keys as part of registering the kind from the start. The variadic
`public ...string` on `Register` is compatible with the call that has no
declaration to make, which is convenient now and is exactly how a kind ends up
silently echoing nothing when someone forgets. Once a real adapter epic lands, a
kind whose settings are all undeclared is worth a boot warning.

## What I still do not understand

Whether a kind should declare its settings rather than just the public ones. A
kind that named every setting it accepts, marking which are credentials, would
also let boot reject a misspelled setting key the way `DisallowUnknownFields`
rejects a misspelled config key — the same failure, one layer down, currently
unguarded. That is a bigger claim than this fix needs, and it belongs to whoever
lands the first adapter with a real settings schema.

## Evidence

The new tests fail against the old predicate. With `echo` reverted to mapping
`redact` over every setting:

```
--- FAIL: TestEffectiveRedactsSettingsAKindDidNotDeclare (0.00s)
    effective_test.go:166: access_key_id = "AKIAIOSFODNN7EXAMPLE", still carries the secret
    effective_test.go:166: secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", still carries the secret
    effective_test.go:166: api_token = "sk-live-abcdef123456", still carries the secret
    effective_test.go:166: password = "hunter2", still carries the secret
--- FAIL: TestEffectiveRedactsWhenNothingIsDeclared (0.00s)
    effective_test.go:190: nil: bucket = "nerve", want it redacted
    effective_test.go:190: declaresNothing: bucket = "nerve", want it redacted
    effective_test.go:190: otherKind: bucket = "nerve", want it redacted
```

And pass against the fix:

```
$ go test ./... 
ok  	github.com/tunedev/nerve/internal/adapters/inbound/admin	0.003s
ok  	github.com/tunedev/nerve/internal/config	0.003s
ok  	github.com/tunedev/nerve/internal/core/vfs	0.003s
ok  	github.com/tunedev/nerve/internal/leak	0.447s

$ make lint
golangci-lint config verify
golangci-lint run
0 issues.
```

The endpoint still answers, and the connection-string password is still gone:

```
$ NERVE_CONFIG=/tmp/nerve-cbe.json ./bin/nerved
nerved dev (go1.27.0)
log level debug
admin http://127.0.0.1:7891/config

$ curl -s http://127.0.0.1:7891/config
{"settings": [
  {"name": "log-level",    "value": "debug"},
  {"name": "postgres-dsn", "value": "postgres://nerve:xxxxx@db:5432/nerve?sslmode=disable"},
  ...
], "mounts": []}
```

`mounts` is empty because `main.go` registers no kinds yet, which is also why
this leak was latent rather than live. The mount path is proven in
`internal/adapters/inbound/admin/admin_test.go`, where the served body is checked
for `AKIAIOSFODNN7EXAMPLE` and does not carry it.
