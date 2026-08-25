# Nerve

The storage and memory substrate. A uniform resource interface, a mount table, and
self-describing implementations.

**The engineering tenets live in `../CLAUDE.md` and govern this repo.** They are not
restated here. This file covers only what is specific to Nerve.

## Status

Scaffolding only. `docs/roadmap.md` is the work: 97 tickets across twelve epics, none
deferred. Start at E1 and E12.14, which lands before E1.6 because the architecture
boundary is a `depguard` rule inside that lint config.

## The settled decision

Capability discovery uses descriptors plus accessors, never type assertion.

Behavioural descriptors — append-only, read-destructive, read-only, broadcast, object-store
— are data returned per path, because capability varies within a single mount and a type
cannot express that. Extra verbs are reached through accessors on the base interface that
return a narrow interface or nil.

This is not a style preference. Every caller holds the mount dispatcher rather than a
plugin, so `fs.(Symlinker)` interrogates the wrapper and never the plugin — the same defect
that forced `http.ResponseController` into the standard library. Accessors on the base
interface force the dispatcher to forward.

## Layout

`internal/core` owns its ports, `internal/adapters` implements them, `cmd/nerved/main.go`
is the sole composition root. Specs in `docs/specs/`, increment notes in `docs/notes/`.
