# nrv-0oxq — the preserved `at()` edit is discarded

The five-line edit held uncommitted in polecat `capable`'s worktree, preserved as
commit `21f0a6f` on `polecat/capable/nrv-rfr+mtcb40fd`, is **wrong**. It is a
cross-user scope escape. Do not land it, do not reuse it.

## What it changed

`at()` resolved a name through the scope in one call:

```go
return v.scope.Path(v.root, name)
```

The edit split that into a base and a join:

```go
base, err := v.scope.Path(v.root, "")
if err != nil {
	return "", err
}
return path.Join(base, name), nil
```

## Why it is wrong

`Scope.Path` anchors `rest` before joining it:

```go
path.Join("/", root, s.User, s.Workspace, path.Join("/", rest))
```

The inner `path.Join("/", rest)` collapses a climbing spelling against `/` first,
so `../../bob` is already `/bob` by the time the outer join runs and it lands
*under* the scope. The edit drops that inner anchoring: `path.Join(base, name)`
cleans base and name together, and the climb eats the scope segments.

The two spellings agree on every ordinary name and diverge on exactly the ones
the scope exists to contain:

```
name                          Scope.Path(root, name)         path.Join(base, name)
"notes.txt"                   /files/alice/w1/notes.txt      /files/alice/w1/notes.txt
"a/b"                         /files/alice/w1/a/b            /files/alice/w1/a/b
"../../../bob/w2/secret"      /files/alice/w1/bob/w2/secret  /bob/w2/secret
"/../../bob"                  /files/alice/w1/bob            /files/bob
"a/../../../../etc/passwd"    /files/alice/w1/etc/passwd     /etc/passwd
```

Applying the edit's semantics to today's `at()` and running the package:

```
$ go test ./internal/core/workspace/...
--- FAIL: TestNoPathSpelledInsideAScopedViewReachesAnotherScope
    --- FAIL: .../Write
        after Write of ../../bob/cv/secret through a view:
          Read("/files/bob/cv/secret") = "alice was here",
          want "bob's salary expectations"
    --- FAIL: .../Create   --- FAIL: .../Remove
    --- FAIL: .../RemoveAll --- FAIL: .../RenameOnto
```

Alice writes into bob's CV through her own view. That is the leak
`Scope.View` was built to close, reintroduced by the function that closes it.

## Why nothing is lost by discarding it

The bead kept the edit on the reading that it might be an unlanded fix to the
same boundary `nrv-rfr` was about. That reading is answered independently:
`5abaac3` (nrv-aeyc, 2026-09-05) landed the real fix, three days after the edit
was stranded, and it *did* move `at()` to a base-and-join shape:

```go
func (v view) at(name string) string {
	return path.Join("/", v.base, path.Join("/", name))
}
```

The base is resolved once in `View` rather than per call, and the join keeps the
inner anchoring the edit dropped. So the edit's one defensible idea — hold the
base rather than re-derive it — is already on `main`, in a form that contains the
climb. What remains in `21f0a6f` is only the defect.

## What surprised me

That the safe and unsafe spellings differ by an inner `path.Join("/", …)` that
reads like a no-op. `path.Join` cleans the *whole* concatenation, so where the
anchoring happens is the entire security property. `Scope.Path` documents the
containment as its own; a caller that splits the call into base plus join has
silently opted out of it while looking like it refactored.

That is the general shape: a helper whose contract is enforced *inside* its body
cannot be safely decomposed by a caller. `Scope.Path` is the only place the
layout is spelled, and this was a second place — the exact failure the package
doc names.

## What I would do differently

Preserving the edit cost one commit and was right. But `check-recovery` reported
`git_state=has_uncommitted, uncommitted_files=1` and nothing more, and a
seven-day-old question about a security boundary was the result. Running the
owning package's tests against a stranded edit at preservation time would have
answered it in one command — the suite already had the coverage.

## Still open

Nothing about this edit. `21f0a6f` and its branch can be deleted; this note is
the record. The general hazard — a polecat exiting with a dirty worktree — is
`nrv-6w0` and is untouched by this.
