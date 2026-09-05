# nrv-aeyc — the scope subtree becomes the plugin's own root

A symlink standing inside alice's subtree and pointing at bob's was followed, at
the port and through a FUSE mount, handing one user another's content first try.
The scope boundary was lexical derivation plus plugin-root containment, and the
two do not compose when the plugin resolves symlinks: `os.Root` contained
`localfs` to the *area*, the scope subtree is inside the area, so the link never
left the root the kernel checks against.

The fix moves the containment root to the scope subtree. `Subber` is a fourth
accessor on `FileSystem` — `Sub(path) FileSystem` — and `Scope.View` prefers it
over path derivation wherever the tree beneath answers.

## Sub is not I/O, and that decided everything else

The obvious shape is `Sub(ctx, path) (FileSystem, error)` returning a fresh
`*os.Root`. It fails on lifetime. `files.Tree` and `search.Routes` build a view
per request, so an eagerly opened root is a descriptor leaked per request; a
cache of them is a root still pointing at a directory that has since been
deleted and made again, which `Lifecycle` supports.

Making `Sub` pure removes all of it. `subFS` holds the mount and a path, and
takes a handle per verb:

```go
opened, err := s.parent.root.OpenRoot(rel(s.at))
defer func() { _ = opened.Close() }()
return act(&FS{root: opened})
```

One `openat` beside the syscall the verb was going to make anyway, no descriptor
between verbs, and a root that is always the directory standing there now.
`TestASubtreeHoldsNoDescriptorBetweenVerbs` counts `/proc/self/fd` across 200
reads; without the `defer` it goes 7 → 209.

Being pure also removed the ambiguity that would otherwise have sunk `View`. A
`MountTable` spans host-backed and invented areas, and its accessors are
table-wide; asked for a subtree it has to answer *per path*, and "this mount has
no narrower root" is a shape and not a failure. With no error to disambiguate,
nil says it, and `View` reads: subtree if there is one, derivation if there is
not — never a fallback triggered by an error that might have meant something
else.

## What derivation is still for

A kind that resolves nothing but the name it was given — `memfs`, `kvfs`,
`vectorfs` — is contained by the derivation alone, completely. They hand out no
subtree and lose nothing. Only a kind that resolves through something else needs
to have been opened on the subtree, which is exactly the set that answers
`AsSubber`.

## The surprise: Mkdir of a view's own root

`Tree.Mkdir("/notes")` on a workspace nobody had created used to work, because
`MkdirAll` over the derived path made the scope subtree on the way. Rooted at
that subtree, the same call could not make the ground it stands on. `subFS.Mkdir`
now makes the subtree first — the port has `Mkdir` create missing parents, and
from inside a subtree the subtree is one — which keeps the behaviour rather than
teaching nine call sites that a workspace must be created first.

## What is still open

A component of the scope path being *itself* a symlink. `OpenRoot("alice/cv")`
follows a link at `alice/cv` as long as it stays inside the area, so a planted
`alice/cv -> ../bob/cv` hands out bob's directory as alice's root — proven, and
filed as `nrv-d42y`. Closing it wants a no-follow open of the scope path, and
`os.Root` exposes no `RESOLVE_NO_SYMLINKS`; an `Lstat` guard would check one
component, race, and cost a syscall on every verb. It is a different mechanism,
not a bigger version of this one.
