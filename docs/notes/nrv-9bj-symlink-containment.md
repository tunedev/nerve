# nrv-9bj — Containment moves from the string to the kernel

A symlink planted in a `localfs` root led out of it. Lexical cleaning collapses
`..` before a path is joined to the root, which is correct and is what
`TestNoPathClimbsOutOfTheRoot` covers, but a symlink is resolved by the kernel
at the syscall, long after any string has been cleaned. `localfs` now holds its
root open as an `os.Root` and names every path below it, so the operating system
refuses the escape.

The escape, as it read before the fix:

```
root := t.TempDir(); outside := t.TempDir()
os.WriteFile(outside/secret, "not yours")
os.Symlink(outside, root/escape)
localfs.New(root).Read(ctx, "/escape/secret")   // "not yours"
localfs.New(root).Write(ctx, "/escape/planted") // lands outside the root
```

Nothing was exposed: no `localfs` mount exists in any running config, which is
why this was fixed before E2.7 starts mounting host paths rather than after.

## What the pattern was

`FS` holds an `*os.Root` instead of a path string. `host(name)` — a
`filepath.Join` onto the root — became `rel(name)`, a path relative to the root
that `os.Root` resolves itself, and the root is the one name it spells `.`.

`os.Root` in Go 1.27 answers nine of the ten port verbs directly, and `Symlink`
and `Readlink` with them: `OpenFile`, `MkdirAll`, `ReadFile`, `WriteFile`,
`Stat`, `Remove`, `RemoveAll`, `Rename` and `Chmod`. Three things had no
equivalent and are bounded deliberately rather than reached around:

| Verb | No `os.Root` method | What it does instead |
|---|---|---|
| `ReadDir` | `Root.Open` returns a handle | opens the directory, `File.ReadDir(-1)`, sorts |
| `Truncate` | a length is set by naming a path twice | opens the entry, `File.Truncate` |
| `OpenWrite` | no `Root.CreateTemp` | draws a name and creates it `O_CREATE|O_EXCL` |

Each of those stays inside the root: the handle is opened through it, and the
temp name is created through it. Reaching for `os.CreateTemp` would have put a
hole in the containment every other verb goes through.

## What surprised me

**`File.ReadDir` does not sort and `os.ReadDir` does.** The package function
sorts by filename; the method on a handle returns entries in host order. The
port promises `ReadDir` sorted by name, and swapping one for the other silently
dropped that. The conformance suite caught it on the first run:

```
--- FAIL: TestConformance/ReadDir_lists_direct_children_sorted_by_name
    dir.go:91: ReadDir("/jobs") = [queued config audit], want [audit config queued]
```

That is the suite from E2.6 earning its keep on a plugin it was not written for.

**`os.Root` will not follow an absolute symlink, so `Symlink` had to change.**
The old `Symlink` wrote the host path of its target, which the host resolved
back inside the root — correct under lexical containment, unfollowable under
`os.Root`, which refuses an absolute link outright. The target is now written
relative to the directory holding the link, so it resolves inside the root from
either side, and `Readlink` resolves it back against that directory. Without
`TestALinkMadeThroughThePortIsFollowedThroughIt` this would have shipped as a
mount whose own links do not work: no conformance case reads through a link.

**The refusal has no sentinel to match.** `os` spells the condition
`errPathEscapes`, unexported, reaching a caller as `path escapes from parent`
inside a `*fs.PathError`. The port promises the `io/fs` sentinels, so the
message is matched and translated to `fs.ErrPermission` — the mount will not
leave itself, which is a refusal and not a missing path. Matching a message is
fragile, and the containment tests assert the sentinel rather than the wording,
so a rewording in `os` fails loudly instead of quietly widening the mount.

**Refusing the escape and refusing the entry are different.** `RemoveAll("/")`
empties the root, and a planted link is one of the entries it deletes. It has to
delete the link and not what the link stands for, which `os.Root.RemoveAll` does
and which `TestEmptyingTheRootDeletesALinkAndNotItsTarget` now pins: this is the
one verb where following the link would destroy another workspace rather than
read one.

## What I would do differently

Reach for `os.Root` in E2.2 rather than writing `host()`. The lexical join is
the obvious shape and it is what every pre-1.24 passthrough looks like, so it
read as finished. The tell was there in the note it shipped with — "containment
is lexical" is a sentence describing a mechanism, not a guarantee, and the
constraint it was answering is about what a caller can reach.

## What I still do not understand

What `os.Root` deliberately does not contain, and whether this plugin should.
The documentation is explicit: a `Root` does not prohibit traversal of
filesystem boundaries, bind mounts, `/proc` special files, or Unix device files,
and on Unix `Chmod` races if the target flips from a regular file to a symlink
mid-operation. A bind mount inside a workspace root is a way back out that this
fix does not close, and closing it is not a filesystem plugin's job — it is the
job of whatever lays the root out. Where that boundary is drawn belongs to
whoever mounts a real user workspace.

## Evidence

The escape, proven against the code as it stood, with the fix not yet written:

```
$ go test ./internal/adapters/outbound/localfs/ -run 'Climbs|Planted|Followed' -v
=== RUN   TestNoPathClimbsOutOfTheRoot
--- PASS: TestNoPathClimbsOutOfTheRoot (0.00s)
=== RUN   TestNoHostPlantedSymlinkLeadsOutOfTheRoot
    containment_test.go:63: Read("/escape/secret") = "not yours", want the mount to refuse a link out of it
    containment_test.go:63: Read("/straight-out") = "not yours", want the mount to refuse a link out of it
    containment_test.go:68: Write through a link out of the mount = nil, want a refusal
    containment_test.go:71: the file outside the root = <nil>, want it never written
--- FAIL: TestNoHostPlantedSymlinkLeadsOutOfTheRoot (0.00s)
=== RUN   TestALinkMadeThroughThePortIsFollowedThroughIt
--- PASS: TestALinkMadeThroughThePortIsFollowedThroughIt (0.00s)
FAIL
```

The lexical case passed the whole time, which is the point: it was never the
hole.

And after:

```
$ go test ./internal/adapters/outbound/localfs/ -v -run 'Climbs|Planted|Followed|Emptying'
=== RUN   TestNoPathClimbsOutOfTheRoot
--- PASS: TestNoPathClimbsOutOfTheRoot (0.00s)
=== RUN   TestNoHostPlantedSymlinkLeadsOutOfTheRoot
--- PASS: TestNoHostPlantedSymlinkLeadsOutOfTheRoot (0.00s)
=== RUN   TestEmptyingTheRootDeletesALinkAndNotItsTarget
--- PASS: TestEmptyingTheRootDeletesALinkAndNotItsTarget (0.00s)
=== RUN   TestALinkMadeThroughThePortIsFollowedThroughIt
--- PASS: TestALinkMadeThroughThePortIsFollowedThroughIt (0.00s)
PASS
```

## What is not fixed here

`internal/vfstest` has no escape case at all, so every future host-backed plugin
inherits the same blind spot. The suite cannot plant a link on its own — memfs
has no host to plant one in — so it needs the plugin to hand over a way to write
behind its back. Filed as nrv-olp.
