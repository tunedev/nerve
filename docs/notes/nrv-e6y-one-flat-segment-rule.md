# nrv-e6y — the flat-segment rule is spelled once

`vfs.ValidSegment(kind, name)` is the rule: a name that can stand as one path
segment is non-empty, carries no separator, and is not `.` or `..`. It had been
written out fourteen times across twelve core packages; all fourteen now call
it.

```
$ grep -rn "names a directory" --include="*.go" internal/ | grep -v _test
internal/core/vfs/name.go:23:  return fmt.Errorf("%s %q names a directory: %w", kind, name, fs.ErrInvalid)

$ make lint
0 issues.

$ make test
go test ./...

$ make race
go test -race ./...

$ go test -count=1 -v ./internal/core/vfs/ | grep -c -- '--- PASS'
254
```

The twelve packages that held a copy pass:
`workspace` 335, `projection` 104, `memory` 96, `relation` 82, `thread` 65,
`session` 64, `kv` 58, `ingest` 43, `library` 35, `resource` 32, `version` 26,
`schedule` 19.

## What the pattern was

**The rule is a path fact, so it lives with paths.** `vfs` already says it holds
"the entities and rules of the storage domain: paths, capability descriptors,
and the small verb set every storage kind answers", and every package that held
a copy already imported it. A new leaf package would have been a thirteenth
import for a rule that has a home.

**The kind is the caller's word, and it is the only parameter that varies.**
Three of the copies were already `validSegment(kind, name)`; the other eleven
were the same body with a noun baked into three format strings. Passing the noun
in is what collapses fourteen into one — `vfs.ValidSegment("ref name", name)`,
`vfs.ValidSegment("predicate", e.Predicate)` — and the error still reads in the
vocabulary of whoever refused it rather than the rule's.

**Two exported wrappers survive because they are contracts, not indirection.**
`version.ValidateRefName` is what `localrefs` and every other ref store calls,
and its doc is a promise about ref stores agreeing. It is now one line over
`vfs.ValidSegment`. The rest of the local helpers were unexported, called once
or twice inside their own package, and became call-site calls.

**A caller with more to say wraps rather than reimplements.**
`projection.Source.valid` and `Manifest.sliceKey` were the two spellings that
carried context the rule does not have — which root the source file is under,
which column a slice key came from. Both keep it by wrapping:

```
source under "/records": source file "a/b" contains a separator
projection "applications" slices by "stage": slice name "." names a directory
```

## What surprised me

**There were fourteen, not the four the ticket counted.** The bead was filed at
three, updated to four, and four notes since then each recorded writing one more
and deferring the extraction. Grepping for `validName|validSegment` finds ten of
them. Grepping for the error text finds the other four: `version.ValidateRefName`
and `memory.validSource`, which are the rule under a name of their own, and two
`switch` statements inline in `projection` validators that had other work to do.
The count in the bead was the count of things named like the rule.

**The `strings` import disappearing is how you find the last copy.** After the
rewrite, `goimports` dropped `strings` from nine files and `fmt` and `io/fs`
from most of the same ones. A file
that still imports `strings` after this is a file with something else in it —
which is exactly how `projection/slice.go` and `projection/projection.go` sorted
themselves into "still needs it" and "was only the rule".

## What I would do differently

Nothing about the shape. The one call I would defend rather than repeat: the
kind label is a string literal at each call site, and `resource` now spells
`"resource name"` in three places. A package-level constant would have made the
label one thing to change, but it would also have put the caller's vocabulary
behind a name instead of in the error, and three literals that read correctly
beat one constant that has to be looked up.

## What I still do not understand

Whether `sidecar name` is the right word for `resource.Store.sidecar`, which
passes its `kind` parameter to the rule. The value is a filename beside a
resource; the parameter is called `kind` because that is what the caller asks
for. The neighbouring error already says "sidecar", so the two now agree — but
the parameter's name still does not.

## Deliberately given up

**The trailing "not a X" clause.** Eight of the fourteen copies ended
`names a directory, not a %s`; six just said `names a directory`. Keeping the
clause needs a second parameter, because the label and the noun differ wherever
the label is a phrase — "ref name" is not "a ref name", it is "a ref". A
two-label helper for one trailing clause is worse than the clause, so the clause
is gone and every message is now `<kind> "<name>" names a directory`.

**Four other messages changed wording.** `library` said "name is empty" and now
says "document name is empty"; `projection` said "projection has no name" and
now says "projection name is empty"; the projection source said "source under %q
names no record file" and now says "source under %q: source file is empty";
`memory` said "memory names no source session" and now says "source session is
empty". Nothing asserts on these strings — every test in the repo checks
`errors.Is(err, fs.ErrInvalid)` — so the wording was free to move, and this
paragraph is the record that it did.
