# nrv-u6xc — naming a workspace inside the scope instead of deriving a path to it

`nrv-aeyc` moved the containment root from the area to the scope subtree.
`nrv-d42y` made the walk down to that subtree refuse a link at every component.
Both went through `Scope.View` and the `Subber` accessor. `Lifecycle` is not on
that path: `Create`, `Delete` and `List` derived a path from `Scope.Path` and
handed it to the mount table, and the tree behind the table resolves it.

With `/files/alice` planted as a link to `/files/bob`, measured through the
table rather than assumed:

```
life.List(ctx, "alice")   = [cv notes]        names bob's workspaces
life.Delete(ctx, alice)   = nil               deletes bob's workspace
table.Read(bob/cv/secret) = "", ErrNotExist   bob's content is gone
```

Cross-user data destruction, not a read leak.

## The fix is a change of who resolves the string, not of the string

`fsys.RemoveAll(ctx, "/files/alice/cv")` and
`userScopeOf("/files/alice").RemoveAll(ctx, "cv")` name the same entry. The
difference is that the first hands the whole path to the tree, which follows a
link at any component of it, and the second reaches the scope first and then
names one entry inside it.

So each verb narrows once with `Scope.View` over user scope — the subtree
holding every workspace a user has — and names the workspace inside it:

```go
held, err := l.holding(s)          // one narrowed area per area
act(area.held, s.Workspace)        // Mkdir / RemoveAll of one name
```

`List` is the same handle with `ReadDir("/")`.

This is `Scope.View`'s existing shape rather than a new one, which is the whole
reason it works across kinds that answer differently. An area that hands out a
subtree hands out the one `localfs`'s no-follow walk reached; an area that hands
out none — `memfs`, `kvfs` — falls back to the derivation, which is the entire
containment a kind that resolves nothing but the name it was given needs. No
plugin changed.

## The one surprise: `Delete` cannot use a view of the workspace

The obvious reading of "act through a view" is a view of the *workspace* scope.
It does not work. `view.RemoveAll("/")` empties the root rather than deleting
it — deliberately, because a view that deleted the scope it stands for would
answer nothing afterwards. `Delete` has to remove the workspace directory, so
the handle it wants is one level up, and then `Create`, `Delete` and `List` all
want the same one. User scope is the handle for all three, which is why they
collapse into one helper rather than three.

## What the error messages cost

Acting on a name inside a narrowed tree means nothing downstream knows the
derived path, so `delete "/info/alice/cv"` would have become `delete "cv" in
"/info"`. `holding` carries the derived path alongside the narrowed filesystem,
used for the message and never resolved, so the wording an operator reads is
unchanged. A test asserting that wording is what caught it.

`Scope.Path`'s two segment checks moved to `Scope.valid`, because a caller that
reaches the tree without deriving a path down to it still needs the answer, and
asking twice in two spellings is how the two come to disagree.

## Tests

`internal/core/workspace/isolation_test.go` gains two cases, both failing when
the fix is reverted: `Create` and `Delete` against a scope whose user directory
is a link, and `List` against a bob who owns a workspace alice does not, so a
leak names something her own areas could not have produced. Each stands on the
positive control the family established — the same link read back through the
table returns bob's content, so a refusal is the walk refusing rather than an
empty world.

## What is still open

Nine more types derive from `Scope.Path` and act on a raw tree the same way:
`session`, `thread`, `memory`, `library`, `resource`, `relation`, `kv`, `ingest`
and `schedule`. Each holds a `vfs.FileSystem` and a root and never reaches a
`Subber`. Whether any is reachable depends on the composition root mounting a
host-backed area beneath it; the derivation offers no containment either way.
Filed as `nrv-ddvp`. Three increments have each closed one caller and left
others — the fourth should probably close the shape rather than the caller.
