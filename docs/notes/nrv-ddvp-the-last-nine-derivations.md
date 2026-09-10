# nrv-ddvp — the last nine stores that derived a path and handed it to the tree

`nrv-u6xc` put `Lifecycle` on the path `nrv-aeyc` and `nrv-d42y` had built, and
named nine more types with the same shape: a `vfs.FileSystem` and a root, a
`Scope.Path` derivation down to the workspace, and no `Subber` anywhere. This
increment is those nine — session, thread, memory, library, resource, relation,
kv, ingest, schedule.

## What it was worth, measured rather than assumed

With `<root>/alice` planted as a link to `<root>/bob` through the mount table,
against a host-backed area, before:

```
--- FAIL: .../Reads/{resource,kv,session,thread,memory,library,relation,ingest}
    reading alice's scope returned "bob's salary expectations", want nothing of bob's
--- FAIL: .../Deletes/session
    after a delete, bob's world is map[], want map[cv/cv/1:... cv/cv/SESSION:...]
--- FAIL: .../Writes/library
    after a write, bob's world is map[cv/cv/SKILL.md:"alice was here"], want bob's
```

Every kind with a scoped read served bob's content to alice. Seven of nine
destroyed his data outright. After: `ok github.com/tunedev/nerve/internal/core/workspace`.

## The shape

The change is uniform because `Scope.View` already was: each store's path helper
stopped returning a string and started returning the view plus the name inside
it.

```go
func (st *Store) dir(s workspace.Scope, name string) (vfs.FileSystem, string, error)
```

Everything below it followed: a helper that took `dir string` and reached
`st.fsys` takes the filesystem as a parameter, which turned most of them into
free functions — they were only methods for the receiver's `fsys`.

## What surprised me

**Two helpers became better code for a reason unrelated to isolation.**
`session.readRecord`, `thread.readRun`, `memory.logSeqs` and `resource.read`
were methods holding one field of their receiver. Passing the tree explicitly
made the dependency visible in the signature, which is the property the tenets
ask for and which the method form had hidden.

**`library.Copy` was the only verb the change asked a question of.** It compared
two derived paths to refuse copying a document onto itself, and there are no
derived paths left to compare. `from == to && name == into` is exactly the same
claim — `Scope` is comparable and `ValidSegment` has already made both names
flat — but it is the one place the string stopped standing in for the identity.

**A test that passes for the wrong reason is one keystroke away.** The first
version of the positive control compared absolute paths under alice's user root
with absolute paths under bob's, which never match, and it reported the live
link as dead. The assertion has to be about what the subtrees *hold*.

## Deliberately left unchanged

`schedule.Log.Unfinished` walks the whole root across every scope and hands each
path to `workspace.Locate` to say which scope it came from. It derives nothing
from `Scope.Path`, so it is not this bug, and it already answers for every
user's jobs by design — no scoped store hands its answer to a caller. But a link
on the scope path would make it report one user's job under another's scope, and
a re-run would then act in the wrong workspace. Filed rather than widened.

## What I still do not understand

Whether `Scope.View` should be the only way to obtain a scoped handle at all —
`Scope.Path` still exists and is still exported, and every one of these ten bugs
was a caller reaching for it. The derivation is genuinely needed (`Locate` is its
inverse, error messages want a place to name), but nothing stops the eleventh
store from starting the same way.
