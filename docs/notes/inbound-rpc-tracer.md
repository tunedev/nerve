# Inbound RPC — a document into a running process

`nerved` takes a document from outside itself. `proto/nerve/resource/v1` is the
contract, `internal/adapters/inbound/rpc` serves it with connect-go on
`api-addr`, and one port answers Connect JSON over HTTP/1.1, gRPC over h2c and
gRPC-Web. `ResourceService` is `resource.Store` with one RPC per verb: `Create`,
`Upload`, `Complete`, `Content`.

Against a running binary with a `localfs` mount under `/resources`:

```
$ curl .../Complete
{"resource":{"name":"cv.txt","contentType":"text/plain","size":"53","digest":"sha256:6181c5d3...","complete":true}}
$ printf 'a document put into a running nerved from outside it\n' | sha256sum
6181c5d3...  -
$ buf curl --protocol grpc --http2-prior-knowledge .../Content   # cv.txt, written over JSON
{"content": "YSBkb2N1bWVudCBwdXQgaW50byBhIHJ1bm5pbmcgbmVydmVkIGZyb20gb3V0c2lkZSBpdAo="}
```

## What the pattern was

An inbound adapter, like `fusefs` and unlike `admin`: every method maps a
request to domain terms, calls one verb, and maps the answer back. `scopeOf`,
`messageOf` and `codeOf` are the only places the two vocabularies meet. No core
file changed.

Errors keep their meaning across the wire the way `fusefs` keeps them across
the kernel: the `fs` sentinels become `NOT_FOUND`, `ALREADY_EXISTS`,
`INVALID_ARGUMENT` and `PERMISSION_DENIED`, `stability.ErrUnavailable` becomes
`UNAVAILABLE`, and everything else is `INTERNAL`. A client decides whether to
retry from the code alone, so the mapping is the classification.

"Ports never speak protobuf" is a lint failure now rather than a sentence:
depguard denies `connectrpc.com/connect`, `google.golang.org/protobuf`,
`google.golang.org/grpc` and `internal/gen` inside `internal/core`. A probe
file importing `internal/gen` from `core/resource` fails `make lint`.

## What surprised me

**connect-go's `simple` option removes the envelope from every signature.**
The default generator wraps each message in `connect.Request[T]` and
`connect.Response[T]`. With `simple`, the handler interface is
`func(ctx, *Req) (*Res, error)`, which is the shape a core verb already has, so
the adapter is only mapping.

**One port for three protocols takes one field.** `http.Server.Protocols` with
`SetUnencryptedHTTP2` is h2c in the standard library, with no `x/net/http2/h2c`
wrapper. The test proves that line matters: with it removed, the gRPC case fails
with `frame header looked like an HTTP/1.1 header` and the other two pass.

**A second listener changed `serve`.** It drained one server. Now it drains a
list, and a server that cannot bind shuts the others down. Otherwise the admin
surface keeps answering ready while the contract it fronts never started.
goleak catches the version that forgets: it reports the orphaned
`ListenAndServe`.

## What I would do differently

Pin `buf` before writing any proto. The generators are `go.mod` tool
dependencies for the reason `golangci-lint` is one, and `buf` pulled upgrades
into modules the linter shares. Adding it first would have kept that churn in
its own step.

## Deliberately not built

- **Authentication.** The scope is whatever the request names, so any caller
  that reaches the port can write into any workspace. `api-addr` is empty by
  default and the README says to keep it on loopback. This is the next
  increment, not an oversight, and it blocks exposing the port.
- **Streaming upload and download.** `Upload` and `Content` move a whole
  document in one message, bounded at 8 MiB. `resource.Store` already streams,
  and a client-streaming `Upload` is its natural shape. It is left out because
  it is a second unknown.
- **The other verb sets.** `SignedDownload`, `Download`, `Delete`, `Stat`,
  `search.Routes`, `ingest.Ledger` and `kv.Space` have no RPC yet.
- **Settings for the bounds.** The 8 MiB message bound and the one-minute
  write timeout are constants in the composition root. Tenet 6 wants them
  configurable.
- **`buf breaking` in `check`.** The contract is versioned, but nothing checks
  compatibility against `main` yet.

## What I still do not understand

Whether gRPC-Web gives a browser anything that Connect JSON does not. Both run
over HTTP/1.1 and connect-go serves both for free. It is kept because the tenet
names it, not because a caller needs it.
