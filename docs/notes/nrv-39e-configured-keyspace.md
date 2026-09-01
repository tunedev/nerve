# nrv-39e — The keyspace is a setting

`kvfs.ObjectStore` landed in E2.8 and nothing could reach it: `cmd/nerved` built
`kvfs.NewMemStore()` inline, so a durable key-value mount existed as a type and
not as anything an operator could ask for. It is now `kv-store-url`, one bucket
URL, and `registerKinds` takes the keyspace rather than building one.

## What the pattern was

The open question was whether a store's connection is one URL-shaped setting or
five named ones. It is one, and the reason is not brevity.

A bucket addressed path-style already *is* a URL: the host is the endpoint, the
first path segment is the bucket, and S3 has nowhere else to put the region, so
it is a query. Write the credentials as userinfo and the whole connection is
`http://key:secret@localhost:9000/nerve?region=us-east-1` — the value with the
credentials taken out is the bucket's real address.

That is the shape `postgres-dsn` and `rabbitmq-url` already have, which is what
made the second half of the ticket disappear. `Config.Effective` replaces the
password inside a URL, so the setting reads back redacted with no new machinery,
and the alternative — five global settings plus a way for one of them to declare
itself a credential — would have been a second redaction mechanism beside the
one mount settings already have.

```
$ curl -s http://127.0.0.1:7891/config | grep -A1 kv-store-url
      "name": "kv-store-url",
      "value": "http://nerve:xxxxx@127.0.0.1:9000/nerve?region=us-east-1"
```

Redaction moved from unexported `redact` to exported `config.Redact` for one
caller: the boot banner. An operator's first question of a process holding state
is whether it is durable, and a second spelling of what a credential looks like
is how one of the two ends up wrong.

## What surprised me

**The validation split was the decision, not the parsing.** `config.Validate`
refuses a URL with no bucket, a path deeper than one bucket, no host, the wrong
scheme, or no credentials; `cmd/nerved` then reads the parts out and checks
nothing. Putting the credential check where it is signed with reads more
naturally in isolation and is wrong here: every other setting reports what is
wrong with it in the one error `Validate` joins, so an operator fixing a config
gets the whole list rather than one problem per restart.

```
$ NERVE_KV_STORE_URL='http://127.0.0.1:9000/nerve/sessions' ./bin/nerved
nerved: kv-store-url has path "/nerve/sessions", want one bucket name

$ NERVE_KV_STORE_URL='http://127.0.0.1:9000/nerve' ./bin/nerved
nerved: kv-store-url carries no credentials; write them as key:secret@ before the host
```

**Empty had to be the default, and the reason is the plane test.** `postgres-dsn`
defaults to a localhost DSN because the stack is what a local checkout runs
against. Doing the same here would have made every `kvfs` mount depend on a
container being up, and turned an in-memory keyspace into the thing you have to
switch off. It is `telemetry-endpoint`'s shape instead: unset is a real answer,
and it is the one a laptop with no bucket gives.

**A deeper path is refused rather than read as a prefix.** `/nerve/sessions`
could plausibly mean "the bucket `nerve`, under `sessions/`". It is refused
because a mount's namespace is already how a keyspace is partitioned, and two
spellings of that would let an operator scope a keyspace twice and find the keys
under neither.

## What I chose not to build

**One keyspace, not a table of them.** Every `kvfs` mount is a part of the one
store the composition root owns. Naming a store per mount would undo the reason
`Factory(store)` closes over one, and there is no second bucket to want yet.

**No boot-time reachability check.** Nothing dials the store at boot, so a wrong
endpoint is found by the first call rather than by the banner. That is the same
trade `objectfs` already makes — `NewS3` resolves the URL and stops — and the
breaker is what a store that is not answering runs into.

## What I still do not understand

Whether the scheme should say which kind of store rather than which transport.
`http://` and `https://` name the wire, and the fact that what answers there is
S3-shaped is carried by the setting's name alone. A second kind of keyspace
would need `s3://` versus something else, and then the endpoint's own TLS choice
needs somewhere to live — `s3+http://`, or a query. Nothing here has a second
kind, so the question is real and not yet answerable.

## Evidence

The acceptance is that a value written through a mount is still there when the
process that wrote it is gone. `TestAValueWrittenThroughAKvfsMountSurvivesARestart`
builds the table twice the way `main` builds it once, over MinIO:

```
$ make up
$ make test-store
ok  github.com/tunedev/nerve/internal/adapters/outbound/objectfs  0.226s
ok  github.com/tunedev/nerve/internal/adapters/outbound/kvfs      0.263s
ok  github.com/tunedev/nerve/cmd/nerved                           0.235s
```

It discriminates. With `keyspace` returning the map whatever the URL says:

```
--- FAIL: TestAValueWrittenThroughAKvfsMountSurvivesARestart (0.00s)
    keyspace_test.go:102: Read(/sessions/abc/state) after a restart = read "/abc/state":
        get "nerved-TestAValueWrittenThroughAKvfsMountSurvivesARestart/abc/state": file does not exist
```

And the mount an operator configured is the object-store one:

```
$ NERVE_CONFIG=nerve.json ./bin/nerved
{"msg":"nerved starting",...,"kv-store-url":"http://nerve:xxxxx@127.0.0.1:9000/nerve?region=us-east-1"}
{"msg":"mount","prefix":"/sessions","kind":"kvfs","caps":{...,"ObjectStore":true}}
```
