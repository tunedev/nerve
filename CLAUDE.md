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


<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:6cd5cc61 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->
