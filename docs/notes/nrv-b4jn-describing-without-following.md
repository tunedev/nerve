# nrv-b4jn — the verb that describes an entry without following it

`Stat` follows a link and reports the entry it stands for, so a port with only
`Stat` has already decided that a link is not a kind of entry. E11.1 found this
from the other end: a FUSE mount tells the kernel a link is a regular file, and
`symlink(2)` through the mount then fails `EIO`.

`Lstat` joins `Symlinker`:

```go
type Symlinker interface {
	Symlink(ctx context.Context, oldPath, newPath string) error
	Readlink(ctx context.Context, path string) (string, error)
	Lstat(ctx context.Context, path string) (FileInfo, error)
}

func Lstat(ctx context.Context, fsys FileSystem, path string) (FileInfo, error)
```

The package function is the call a caller writes. Reaching for the accessor
directly leaves every kind that refuses links undescribed, when what it refused
was links and not description.

## Why not the base interface, and the test that says so

The three options on the ticket were a verb on the port, a `Describer`
accessor, and a link target carried on `FileInfo`.

The port was already closed to the first, by a test written before the question
came up:

```go
// filesystem_test.go
func TestInterfaceIsTenVerbsPlusDiscovery(t *testing.T)
```

It reflects over `FileSystem` and compares against ten named verbs plus the five
discovery methods. An eleventh verb fails it. That is the settled decision
holding a case it was not written for: a new capability arrives as a descriptor
or an accessor.

A `Describer` accessor would have cost a fifth accessor on `FileSystem`, on
`NoAccessors`, and on every wrapper, to carry a verb whose entire subject
matter is links. `Symlinker` is already the link verb set, and only a kind with
links can answer `Lstat` differently from `Stat`.

A link target on `FileInfo` was the one that would have leaked. Every wrapper
that derives paths — `workspace.view`, `proxyfs`, `MountTable` — would have to
map that target back into its own vocabulary on `ReadDir` and `Stat` as well as
on `Readlink`. `Readlink` stays the one path-carrying answer, and
`workspace.view.from` stays the one place that refuses a target outside the
scope.

The split that falls out: `Lstat` says *what an entry is*, `Readlink` says
*where it points*. A link out of a scope is described as a link and its target
is refused, which is the containment E5.4 wants — a caller learns not to follow
without learning another user's layout.

## The one asymmetry, deliberately

`tableSymlinker.Symlink` and `Readlink` refuse a path whose mount has no link
verbs, with `fs.ErrInvalid`. `Lstat` does not: it falls through to that mount's
own `Stat`.

```go
func (s tableSymlinker) Lstat(ctx context.Context, name string) (FileInfo, error) {
	m, rest, err := s.table.resolve(name)
	if err != nil {
		return FileInfo{}, err
	}
	return Lstat(ctx, m.fsys, rest)
}
```

A mount table answers the accessor when *any* mount has links, so refusing here
would leave a mixed table unable to describe most of its own paths. The fallback
is not a special case: it is the package function, and it is true for the same
reason there — a kind with no links cannot have one at that path.

## What surprised me

**Two reflective tests caught the new verb before any consumer did.**
`TestEveryVerbThatSendsIsCovered` walks the port for methods taking a context
and fails on one the timeout coverage list does not send;
`TestEveryVerbIsEitherAChangeOrNot` fails on one neither classified as a change
nor as a read. Both fired on the first `go test`, naming `Lstat`. Adding a verb
to an *optional* set is not cheaper than adding one to the port as far as those
two are concerned, which is the point of writing them by reflection.

**`os.Root` has `Lstat`.** The localfs side is four lines and stays inside the
containment every other verb goes through — no `os.Lstat` on a joined path, no
hole beside the root.

**Nine forwarders and none of them had to think.** `guarded`, `bounded`,
`retrying`, `telemetered`, `observed`, `tableSymlinker`, `proxyfs`,
`workspace.view` and the workspace's `areaRoot` each gained one method on a
`Symlinker` wrapper struct they already had. `readingAhead` needed no change at
all: it hands back the plugin's own `Symlinker` unchanged, because there is
nothing to read ahead of in a link verb.

The accessor-on-the-base-interface rule is what makes that mechanical. Every one
of those already had somewhere to hang the method, because none of them could
rely on a caller's type assertion reaching past the wrapper to the plugin.

## What I would do differently

Write the conformance cases before the interface. I added the method, fixed six
compile errors across the wrappers, and only then wrote `vfstest` — so for about
twenty minutes the verb existed and nothing said what it meant. The cases are
three lines of table each and would have been the cheapest possible statement of
the contract.

## What I still do not understand

**Whether `fusefs` should now serve links.** The verb it was missing exists, but
`Lookup` and `Getattr` still go through `Stat`, and the mount's
`TestAPlantedLinkIsServedAsWhatItStandsFor` still passes unchanged. Making the
mount serve `S_IFLNK` and answer `readlink(2)` is E11.3 and E11.4's, and it is
where escape item 1 comes back: a target the kernel resolves in host vocabulary.

**What `Caps` should say about links.** A kind can have the link verbs and still
refuse them at a path, and nothing describes that yet. `Lstat` reports what an
entry is; whether a caller may make one there is a different question and no
descriptor answers it.

## Evidence

The conformance suite, which is where the contract is written:

```
$ go test ./internal/adapters/outbound/localfs/ -run Conformance -v
--- PASS: TestConformance/Lstat_describes_a_link_and_not_what_it_stands_for
--- PASS: TestConformance/Lstat_of_an_entry_that_is_not_a_link_is_Stat
--- PASS: TestConformance/Lstat_of_a_missing_path_does_not_exist
```

Dispatch and the fallback, through the table and through a scope view:

```
$ go test ./internal/core/vfs/ ./internal/core/workspace/ -run 'Lstat|IsSymlink' -v
--- PASS: TestLstatOfAKindWithNoLinksIsItsStat
--- PASS: TestLstatOfAKindWithLinksReachesTheLinkVerbs
--- PASS: TestFileInfoIsSymlinkFollowsMode
--- PASS: TestLstatOfAPathWhoseMountHasNoLinkVerbsIsItsStat
--- PASS: TestLstatOfAPathWhoseMountHasLinkVerbsReachesThem
--- PASS: TestLstatThroughAViewDescribesTheLink
--- PASS: TestLstatThroughAViewDescribesALinkOutOfTheScopeWithoutNamingItsTarget
ok  	github.com/tunedev/nerve/internal/core/vfs
ok  	github.com/tunedev/nerve/internal/core/workspace
```

The gates on the branch:

```
$ go build ./...                      # rc=0
$ go vet ./...                        # rc=0
$ go test ./...                       # rc=0
$ make fmt-check                      # rc=0
$ make lint
go tool golangci-lint config verify
go tool golangci-lint run
0 issues.
$ make race                           # rc=0, whole suite under -race
```
