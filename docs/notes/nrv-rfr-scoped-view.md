# nrv-rfr — The scope becomes a filesystem

A symlink target under another user's subtree, inside the mount both users
share, satisfied every layer beneath it. E5.4 recorded the gap; this closes it
with `Scope.View`, a `vfs.FileSystem` whose own root is the scope root and whose
every path — both ends of `Rename` and `Symlink` included — is derived by
`Scope.Path`.

The escape, as it read before the fix:

```
alice.Path("/files", "peek")   ->  /files/alice/cv/peek
symlink target /files/bob/cv   ->  clean, one mount, inside the localfs root
read /files/alice/cv/peek/secret  ->  bob's content
```

A caller handed the mount table derives its own paths and is contained only
while it does. `Symlink` takes a target nothing dispatches, so that one path was
the caller's to spell freely. A caller handed a view has no spelling for it: the
derivation is the view's, and a path that climbs lands on the scope root.

Nothing was exposed. No caller had been handed a scope-derived filesystem yet;
`library.Registry` derives its own paths with `Path` and takes no target from a
caller.

## What the pattern was

Thirteen forwarding methods and three accessors, each deriving its paths with
`v.scope.Path(v.root, name)` and handing them on. The view stores nothing and
resolves nothing, which is the same shape `MountTable` and `proxyfs.FS` already
have — resolve the path, forward, map anything that comes back.

Three rules are the view's own rather than the tree's beneath:

| Verb | At the scope root |
|---|---|
| `Remove` | refused; a view that deleted its own ground answers nothing after |
| `RemoveAll` | empties the root rather than deleting it |
| `Rename` | refused, for the reason `Remove` is |
| `Readlink` | a target outside the scope has no name here and is refused |

`Readlink` refusing rather than handing the spelling back is the one place this
differs from `proxyfs`, which returns a target it cannot name unchanged. A proxy
has no secret to keep; a scope does, and a path is observable even when what
stands at it is not.

## What surprised me

**`proxyfs` already is this, and the architecture boundary is why it is not
reused.** `proxyfs.New(table, base)` with `base` from `Path` would have been one
line and would have behaved correctly. `internal/core` may not import an adapter
(E1.6), and the view's rule is `Path` rather than a prefix, so the arithmetic is
written twice. Extracting a shared subtree view into `internal/core/vfs` is the
shape that removes the duplication, and it is deliberately not done here:
`internal/core/vfs` is carrying three other in-flight changes, and a third case
would earn the extraction better than an argument does.

**The root rules needed three different kinds beneath to be provable.** Running
the conformance suite over a view is what asserts them, and each kind hides a
different one. Over `memfs` and over `localfs`, `Rename` of the root passes with
the guard removed: every path a view can spell is inside its own root, so
renaming the root always targets its own subtree and the store refuses it
anyway. Over `kvfs` it does not — a keyspace moves a prefix happily, and the
whole workspace lands one level deeper. The guard is only earned once the suite
runs over a kind whose names are prefixes.

**The view is a `FileSystem`, so it owed the suite.** No decorator in the tree
runs `vfstest` — `WithTimeout`, `WithRetry` and `MountTable` all have cases of
their own — and the cases written for this one asked about paths, accessors and
`Readlink`, and never about the root. Handing the view to `vfstest.Run` is what
asks the questions a caller inside one would eventually ask.

## What I would do differently

Run the mutation over every kind before believing a guard. The `Rename` guard
was written from `proxyfs`'s precedent, and it took three attempts to find a
world where removing it fails — which means for two of those attempts it was
decoration that happened to be correct.

## What I still do not understand

Whether the areas should be a view rather than one view per area. A caller
inside a workspace holds one view per area today — files, vectors, keys — and
`Scope.Path` takes the area root because that is how the lifecycle deletes a
workspace across all of them. A single view over every area, with the areas as
its top-level names, is what E11.1 will want to hand a process: an ordinary
directory tree with no way to spell anything above it. That view has to
synthesise its own root listing, and whether that belongs here or in the FUSE
adapter is E11's question rather than this one's.

Whether `Lifecycle` should be the thing that hands out views. It already knows
every area a workspace occupies, and it is the only place that list is written
down. A `Lifecycle.Open(scope)` returning the areas as views would keep it that
way; nothing needs it yet, and a second caller is what should decide.

## Evidence

Green:

```
$ go test ./internal/core/workspace/
ok  	github.com/tunedev/nerve/internal/core/workspace	0.114s
```

Discriminating. Leaving the symlink target underived — the bug itself:

```
--- FAIL: TestALinkMadeThroughAScopedViewCannotReachAnotherScope
    Read through a link to "/files/bob/cv" = "bob's salary expectations",
      want nothing of bob's reachable
```

Deriving the path with `path.Join(base, name)` instead of `Path`, which drops
the clamp:

```
--- FAIL: TestNoPathSpelledInsideAScopedViewReachesAnotherScope
    --- FAIL: .../RenameOnto   --- FAIL: .../Write    --- FAIL: .../Create
    --- FAIL: .../Mkdir        --- FAIL: .../Remove   --- FAIL: .../RemoveAll
    --- FAIL: .../Chmod
--- FAIL: TestALinkMadeThroughAScopedViewCannotReachAnotherScope
--- FAIL: TestNoReadInsideAScopedViewReturnsAnothersContent
--- FAIL: TestNoListingInsideAScopedViewNamesAnotherScope
--- FAIL: TestNoRenameThroughAScopedViewCarriesAnEntryBetweenScopes
```

Removing the root guards:

```
--- FAIL: TestConformance/over_a_tree_in_memory/Remove_of_the_root_is_invalid
--- FAIL: TestConformance/over_a_tree_in_memory/RemoveAll_of_the_root_empties_it
--- FAIL: TestConformance/over_a_keyspace/Rename_of_the_root_is_invalid
```

`TestTheMountTableAloneDoesNotHoldTheScopeBoundary` still passes, and is meant
to: a caller holding the table can still spell the target. That is why a caller
is handed a view.
