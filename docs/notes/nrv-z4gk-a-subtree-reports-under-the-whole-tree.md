# nrv-z4gk — a write through a subtree is reported under the whole tree's path

`WithWriteObserver` goes over the mount table so the path an observer hears is
one it can read back. Its `AsSubber` wrapped each subtree in a fresh `observed`,
and a subtree's paths are its own: a write to
`/resources/ada/hunt/transcript.txt/meta.json` through `resource.Store` (which
writes through `Scope.View`, which narrows via `Subber`) reached the emitter as
`transcript.txt/meta.json`. The listener is bound to `resources.#`, so the
broker routed every job nowhere and no upload was ever ingested.

Measured against origin/main with a throwaway Postgres and RabbitMQ:

```
origin/main   TestAFinalisedDocumentIsIngestedAndFoundByMeaning  FAIL  nothing ingested in 20s
this branch   TestAFinalisedDocumentIsIngestedAndFoundByMeaning  PASS  0.09s (x4)
```

## The fix is at `SubOf`, not in the observer

`SubOf` dropped the path the subtree was asked for before calling the wrapper,
so no wrapper could know where its subtree stands. It now hands `wrap` that
path. `observed` keeps it as `base` and reports `path.Join(base, name)`; nested
subtrees join their bases. The four wrappers that name no path outward
(retry, timeout, breaker, readahead) ignore it.

## What surprised me

The observer's own tests covered every verb and still could not see this: the
`writable` stub handed out no subtree, so `AsSubber` was never exercised. The
new case drives every mutating verb through a subtree of a subtree.

## Left alone, filed as nrv-j7py

`telemetered` has the same shape. `Call.Path` is documented as joining with
`Mount` to give the caller's path, and a subtree's calls record a
subtree-relative path. `SubOf` now makes that fix a one-line change; it is not
made here.
