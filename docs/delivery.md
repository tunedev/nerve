# Delivery

Nerve work reaches `main` through a pull request that tunedev merges. Nothing else
merges. The refinery is stopped and its patrol disabled; `gt done` still submits an
MR bead, and that bead is the record that work is waiting, not an instruction to
merge it.

    polecat delivers branch -> mayor verifies -> mayor opens PR -> tunedev merges

The mayor never runs `gh pr merge`.

The bead closes after the merge, not before. Nothing else closes it: `gt done`
force-closes only when a branch has no commits and therefore no merge request, and
the refinery that would otherwise close on merge is stopped. So a delivered bead
stays hooked until tunedev merges the PR and the mayor closes it with the merge as
the evidence.

This covers nerve only. Fixes to gastown and beads keep the direct-commit flow,
because they unblock the town that reviews this.

## What a PR body contains

Four sections, in this order. Write them for someone deciding whether to merge
without opening the diff.

    ## What changed
    One or two sentences in the imperative, describing behaviour now. The same
    voice as the commit title: "Delete until the store has nothing left, not
    until a listing is empty."

    ## Why it was wrong
    The defect, and the evidence that proved it. Name the mechanism, not the
    symptom. If the old code passed its tests, say why they passed.

    ## Evidence
    Pasted command output. `make check` including race and repro. A claim of
    green without output is not evidence.

    ## Deliberately not done
    What was left out and why. Refusals, deferred scope, a pattern kept rather
    than fixed. Empty is a valid answer; the heading stays.

    Closes <bead-id>

## Rules

- One bead per PR. A PR that closes two beads was two pieces of work.
- The title is the commit title. Imperative, describes behaviour, no bead ID.
- No emoji, in the PR or the commits.
- The branch is already rebased on `main` and its gates are green before the PR
  opens. A PR is a request to merge, not a request to start reviewing.
- Change requests come back as a new commit on the same branch, never a force-push
  over the reviewed history.
