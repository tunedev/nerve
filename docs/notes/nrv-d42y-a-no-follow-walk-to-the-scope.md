# nrv-d42y — opening the scope path without following a link on it

`nrv-aeyc` moved the containment root from the area to the scope subtree, which
closed a link standing *inside* one scope and pointing at another. It left the
other half open: a link standing *on the path to* the subtree.

`os.Root` follows a symlink that stays inside the root, and the scope subtree is
inside the area, so `s.parent.root.OpenRoot("alice/cv")` on a planted
`alice/cv -> ../bob/cv` handed out bob's directory as alice's containment root.
Every verb through the view then read and wrote bob's subtree, and the view could
not tell — each one answered normally. Any component counts, not only the last:
`alice -> bob` is the same escape one level up.

## Lstat is the primitive, and it is not the whole check

`nrv-b4jn` landed `Lstat` on the `Symlinker` accessor, which is the no-follow
describe the earlier note said was missing. Used on its own it is still a guard
that races: `Lstat` says directory, the planter swings a link into place, and the
`OpenRoot` that follows lands on the target.

What makes it an answer rather than a guard is comparing it against the handle
already open:

```go
named, _ := held.Lstat(step)    // the entry, without following a link
landed, _ := opened.Stat(".")   // where opening that entry actually landed
if !os.SameFile(named, landed) { ... }
```

Both answers come from handles, not from names, so a link swung into place makes
the two disagree in either direction and the walk stops. A racing planter can
cause a spurious refusal; it cannot cause an acceptance. A directory has no hard
links, so a link and the directory it stands for are never the same file.

`os.Root` has no `RESOLVE_NO_SYMLINKS` and no way to build a root from a
descriptor, so a no-follow open of a whole path has to be spelled as a walk:
`descend` opens one component at a time and checks each. That is the same number
of `openat` calls `os.Root` was making internally anyway, plus an `lstat` and an
`fstat` per component.

## Mkdir was the second symptom

`under` is not the only place naming the scope path. `subFS.Mkdir` makes the
subtree first — `nrv-aeyc`'s "surprise" section — and it did that with
`MkdirAll` through the parent root, which follows links too. So a view whose
`alice` component was a link *created a directory inside bob's subtree* before
the read was refused: a write into another user's workspace, not just a read out
of one. `makeScope` is the same walk with the missing component made at each
step, so a subtree is made where the mount holds its path and never where a link
points.

## Three layers of test, each failing when the fix is reverted

- `internal/vfstest`: `Sub of a path standing on a link serves nothing it stands
  for`. It belongs in the conformance suite because it is a promise `Subber`
  makes, not a `localfs` quirk — the next kind that resolves names through
  something else inherits the case for free.
- `internal/adapters/outbound/localfs`: both components, every verb, and the
  host checked afterwards for an entry that should never have been written.
- `internal/core/workspace`: the acceptance, on the layer a caller actually
  holds. The link is planted *through the mount table* and read back through it,
  which is the positive control `nrv-aeyc` established: a refusal through the
  view is the view refusing, not an empty world.

With `openScope` reverted to a plain `OpenRoot`, the workspace case does not
merely leak — `RemoveAll` through alice's view **destroys bob's secret**. That is
the sharpest statement of what this was.

## What is still open

`Lifecycle` does not go through a view. `Create`, `Delete` and `List` act on
derived paths through the mount table, so with `/files/alice` planted as a link
to `/files/bob`: `List("alice")` names bob's workspaces, and
`Delete({alice, cv})` deletes bob's. Measured, not assumed. It is the same root
cause and a different mechanism — the fix here is a `Subber` walk, and
`Lifecycle` spans areas whose kinds hand out no subtree at all. Filed separately;
this increment does not half-close it.
