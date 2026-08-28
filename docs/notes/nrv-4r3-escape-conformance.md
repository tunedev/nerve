# nrv-4r3 — The suite asks for the host directory instead of a filesystem

`internal/vfstest` had no escape case, so every host-backed plugin after
`localfs` inherited the blind spot `nrv-9bj` found in it. The suite could not
write one on its own: planting a symlink means reaching behind the port, and
`memfs` has no behind.

`vfstest.Plugin` now has a second constructor. A kind that stores its own tree
keeps `New` and hands over a filesystem; a kind backed by a host directory sets
`Host` instead and is handed a directory the suite made:

```go
vfstest.Run(t, vfstest.Plugin{
    Host: func(t *testing.T, dir string) vfs.FileSystem { return mustNew(t, dir) },
})
```

That inverts who owns the ground. The suite knows where the mount's storage
sits, so it knows what lies outside it, and the five escape cases run without
the plugin implementing a plant hook of its own.

## What the pattern was

The existing gates are all `Caps` predicates — a case asks what the plugin
claims at a path and skips when the claim is absent. Containment is not a
capability: it is not something a mount varies by path, and adding a descriptor
for it would have put "am I safe" in the same vocabulary as "am I append-only",
where a plugin could claim it. Handing over the directory is a claim a plugin
cannot make falsely — either the suite has a host path to plant in or it does
not — so `world.host` is empty for every other kind and the cases skip on it,
the way `AsSymlinker` returning nil skips the link cases.

The five cases, and what each one is for:

| Case | Catches |
|---|---|
| A path out of the root reads nothing outside it | a raw `filepath.Join(root, name)` |
| A path out of the root writes nothing outside it | the same, on the write side |
| A planted link out of the root is not read through | lexical containment |
| A planted link out of the root is not written through | the same, on the write side |
| Emptying the root deletes a planted link and not its target | a `RemoveAll` that resolves before it deletes |

They call the plugin directly rather than through a mount table. That is
deliberate: `MountTable.rooted` cleans `..` out of every path before dispatch,
so a plugin tested only through it is never asked the question. The next caller
may be a test, a replay, or another mount.

## What surprised me

**A test that passes proves nothing about the test.** All five passed against
`localfs` on the first run, which is what `os.Root` promises and also what a
case asserting nothing would do. Proving they discriminate meant restoring the
plugin as it stood before `nrv-9bj` — `git show c945448` into a throwaway
package — and running the suite against it. Three of the five failed there. The
two climbing cases passed, because that version cleaned lexically too; dropping
the `path.Join("/", name)` from its host mapping failed those as well, and
following symlinks in its `RemoveAll` failed the fifth. Each case has now been
seen to fail for the reason it exists.

**The relative climb has to be computed, not written.** A case cannot spell
`../secret` and mean anything, because it does not know how deep the plugin's
directory sits. It plants the entry in a `t.TempDir()` of its own and asks
`filepath.Rel` for the path between the two, which is a sibling hop here and
would be any number of steps elsewhere.

## What I would do differently

Nothing about the shape. The one thing worth saying is that `New` and `Host`
are alternatives rather than a pair, and the type does not say so — a plugin
that sets both gets `Host`, silently. Naming them for the same job in different
vocabularies is the only signal, which is weaker than a compiler error.

## What I still do not understand

Whether the escape cases should run at all for a host-backed kind that is not a
passthrough. `objectfs` over MinIO has a host in the sense that its store is
outside the process, but no directory the suite could plant a link in, so it
keeps `New` and skips these. That is the right answer for MinIO and it may be
the wrong answer for a kind that has both — a cache with a disk spill, say —
where the escape surface is real and `Host` names only half of it.

## Evidence

Against `localfs` as it stands:

```
$ go test ./internal/adapters/outbound/localfs/ -run 'TestConformance/(A_path|A_planted|Emptying)' -v
=== RUN   TestConformance
--- PASS: TestConformance/A_path_out_of_the_root_reads_nothing_outside_it (0.00s)
--- PASS: TestConformance/A_path_out_of_the_root_writes_nothing_outside_it (0.00s)
--- PASS: TestConformance/A_planted_link_out_of_the_root_is_not_read_through (0.00s)
--- PASS: TestConformance/A_planted_link_out_of_the_root_is_not_written_through (0.00s)
--- PASS: TestConformance/Emptying_the_root_deletes_a_planted_link_and_not_its_target (0.00s)
PASS
```

Against the plugin as it stood at `c945448`, before `os.Root`:

```
=== RUN   TestConformance/A_planted_link_out_of_the_root_is_not_read_through
    escape.go:112: Read("/straight-out") = "not yours", want the mount to refuse a link out of it
    escape.go:112: Read("/escape/secret") = "not yours", want the mount to refuse a link out of it
=== RUN   TestConformance/A_planted_link_out_of_the_root_is_not_written_through
    escape.go:123: Write("/straight-out") = nil, want the mount to refuse a link out of it
    escape.go:123: Write("/escape/planted") = nil, want the mount to refuse a link out of it
    escape.go:128: the entry outside the mount = <nil>, want it never written
    escape.go:130: the entry outside the mount = "mine now", want "not yours"
```

With its host mapping no longer cleaning the path:

```
=== RUN   TestConformance/A_path_out_of_the_root_reads_nothing_outside_it
    escape.go:69: Read("../002/secret") = "not yours", want the mount to refuse a path out of it
    escape.go:69: Read("/../002/secret") = "not yours", want the mount to refuse a path out of it
=== RUN   TestConformance/A_path_out_of_the_root_writes_nothing_outside_it
    escape.go:83: Write("../002/secret") = nil, want the mount to refuse a path out of it
    escape.go:87: the entry outside the mount = "mine now", want "not yours"
```

With its `RemoveAll` resolving each child before deleting it:

```
=== RUN   TestConformance/Emptying_the_root_deletes_a_planted_link_and_not_its_target
    escape.go:144: the link inside the mount = <nil>, want it deleted
    escape.go:146: the entry outside the mount = open .../002/secret: no such file or directory, want it untouched
```

Gates:

```
$ make lint
0 issues.
$ go test ./...
(all packages ok)
```

## What is not changed here

`localfs/containment_test.go` stays. The suite asserts that an escape is
refused; those tests assert it is refused as `fs.ErrPermission`, which
`localfs` alone promises because it matches the wording of an unexported `os`
error to get there. That is exactly what a plugin keeps when the suite covers
the port.

`nrv-olp` asks for the same thing as this ticket and is covered by it.
