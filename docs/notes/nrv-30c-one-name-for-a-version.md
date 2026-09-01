# nrv-30c — one name for a version

`version.ID` is `cas.Digest`. A version is one object, its ID is the digest of
the manifest that says what it holds, and there is now one type for that rather
than two. The six hand conversions E4.3 wrote are gone, and the two places that
read a version out of a mount parse it there instead.

```
$ grep -rn ParseDigest internal/core/version/*.go | grep -v _test

$ go test -count=1 ./internal/core/version/ -v | grep -c '^--- PASS'
24

$ go test -race -count=1 ./internal/core/cas/ ./internal/core/version/ \
    ./internal/adapters/outbound/localrefs/ ./internal/adapters/outbound/treecopy/
ok      github.com/tunedev/nerve/internal/core/cas       1.015s
ok      github.com/tunedev/nerve/internal/core/version   1.024s
ok      github.com/tunedev/nerve/internal/adapters/outbound/localrefs    1.015s
ok      github.com/tunedev/nerve/internal/adapters/outbound/treecopy     1.011s

$ make lint
0 issues.
```

## What the pattern was

**The calibration rule, applied to a type instead of an interface.** E4.1
refused a port because no second implementation could be named; E4.2 took one
because it could name a Postgres refs table. The same burden decided this: name
the second consumer of a ref that is not a version, and the opaque `ID` is
earned. E4.4 went looking — a working copy's `Base` is a name standing for
something, read and written exactly as a ref is — and what it stands for is a
version. Nothing could name one, so the genericity was an interface tax.

**The parse moved to where bytes enter.** `localrefs.read` and `treecopy.Base`
now parse what they read, and a manifest's digests are parsed by
`cas.Digest.UnmarshalText` when the record is decoded. Those are the only three
places a digest can arrive from outside this process, and each one refuses a
value that was never a digest at the point the bad value is, rather than handing
a string on for the first reader of a version to fail on.

**`ID` stayed a word.** An alias, not a defined type: `type ID cas.Digest` will
not convert to or from a `cas.Digest` at all, because non-exported field names
from different packages are never identical, so the choice was between an alias
and deleting the domain's name for the thing. The alias keeps `Resolve` saying
it returns a version.

## What surprised me

**`omitzero` was load-bearing, and `omitempty` would have changed every root
version's ID.** `encoding/json` omits an empty string; it does not omit a zero
struct. So the moment `Parent` stopped being a string, a version with no parent
started recording `"parent":""` — different manifest bytes, and since a version's
ID is the digest of those bytes, a different ID for every root version ever
written. The test says so out loud:

```
$ go test ./internal/core/version/ -run TestAVersionRecordsItsDigestsAsText
--- FAIL: TestAVersionRecordsItsDigestsAsText
    the version records {"parent":"","message":"one file",...},
    want a root version to say nothing about a parent
```

That is a one-word tag deciding whether every stored version keeps its name.

**Nothing had to become a spelling.** `SetBase` with the zero ID is reachable:
commit an unchanged empty copy and the versioner records that the copy still
stands nowhere. With `String` producing `":"` for the zero digest, that copy
wrote a state file it could never read back. So the zero digest spells itself as
the empty string and `ParseDigest("")` returns it — `String` and `ParseDigest`
are total inverses, the zero included, and standing nowhere survives being
written down.

## What I would do differently

Nothing in the change, but the bead should have been closed when it was filed
rather than after four rounds of agreeing. The decoupling reading was
respectable in the abstract and was refuted the first time a consumer existed;
what settled it was E4.3's own note saying an ID *is* the spelling of a digest.
The evidence arrived one ticket after the question, and the cost of waiting was
two more packages constructing an ID from bytes with nothing checking.

## What I still do not understand

Whether the ref store should be parsing at all. `localrefs` now imports `cas` to
read its own files, which is a ref store knowing the hashing scheme — exactly
what the opaque `ID` was protecting. The honest answer is that it does not know
the scheme, only that a ref holds a digest, and `ParseDigest` accepts any
algorithm precisely so that a spelling written by another build is not this
one's to reject. But a Postgres ref store will make the same call, and if a
third does something different the parse belongs behind a verb in `version`
rather than repeated in each store.

`internal/core/resource` keeps a digest as a string in its meta record and
swallows the `ParseDigest` error, so a corrupt one reads as "no digest yet"
(nrv-p5d2). Same shape, different domain, and now cheaper to fix than it was:
`cas.Digest` marshals as its own spelling, so the field can change type without
changing the record.
