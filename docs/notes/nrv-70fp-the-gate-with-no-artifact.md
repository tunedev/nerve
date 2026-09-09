# nrv-70fp — The gate with no artifact

`make delivered` lists every delivered branch whose work is not on `main`, with
the open pull request that covers it or `no-pr` where none does. Today it prints
one row.

## What the pattern was

The bead opened on the theory that machinery was bypassing the reviewed-PR route
PR #1 established. It was not. The refinery is deliberately stopped, every sling
took the default `--merge=mr`, and the fourteen commits that reached `main`
without a PR were all committed by tunedev by hand — a human landing finished
branches rather than let them sit, because the third step of the route, "mayor
opens PR", had nobody performing it.

The route works now. Since 2026-08-28 nothing has reached `main` outside a merge
commit:

    $ git log --first-parent --no-merges --oneline 65425d6..origin/main | wc -l
    14
    $ git log --first-parent --no-merges --format='%cd' --date=short 65425d6..origin/main | awk '$1 > "2026-08-28"' | wc -l
    0
    $ git log --merges --oneline 65425d6..origin/main | wc -l
    56

So the remaining defect is not the route, it is that the route's failure has no
shape. A completion nobody opened a PR for and a completion that was never
delivered look identical from every list anyone reads. nrv-q9w sat finished and
unmerged for a day; three sibling completions from the same window landed and
looked fine, and the absence of a PR was not something a person could see. It
took watching the refinery queue drain to zero with the work still missing, over
three cycles, and a deliberately constructed test.

The listing is the missing artifact. Two inputs — branches on the remote, open
PRs on the forge — and the defect is the difference between them.

## What surprised me

**Every cheap definition of "landed" is wrong, and each is wrong differently.**
148 polecat branches, zero open PRs, one genuinely stranded completion. The
naive filters find far more than one:

| Rule | Rows | Why it is wrong |
|---|---|---|
| Tip not reachable from `main` | 10 | Work landed by hand keeps an unreachable branch |
| `git cherry` finds unapplied patches | 7 | Hand-landed commits were edited, so patch ids differ |
| No commit subject matching `(nrv-x)` | 5 | One subject reads `(nrv-15y, gt-pvx safety net)` |
| Neither reachable nor named | 1 | Matches ground truth |

Reachability alone over-reports because the fourteen hand-landed commits left
their branches behind; bead names alone over-report because a merged branch's
own commit need not spell its bead the way the regex expects. Together they
under-report nothing observed here and agree with what `bd` says: the one row is
`polecat/slit/nrv-301+mtpwsegp`, pushed twenty-five minutes before this was
written, bead still `in_progress`.

**The false positives were the useful part.** They are the record of the
incident: nine of the ten unreachable branches are landed work, and every one is
a completion that went around the gate on 2026-08-28. A check tuned until it
prints nothing would have thrown that away.

**A "no-pr" row is not always a defect.** slit's branch was twenty-five minutes
old and nothing was wrong with it. The list is a queue of what the mayor owes a
PR, not an alarm; a row that is hours old is the failure the bead describes. The
row proved that on its own within the hour — a PR was opened for it while this
note was being written, and the same row now reads `#58`:

    $ ./scripts/delivered
    nrv-301    #58          polecat/slit/nrv-301+mtpwsegp

## What I would do differently

I would rather this were not a shell script. It is fifteen lines of `sh`
around `git ls-remote` and `gh` in a repo whose tenet is Go end to end, and it
sits outside `check` because it reads the network. That is the right trade for
an operations listing nothing else depends on — but if a patrol ever consumes
it, the output should become machine-readable before it grows a second consumer
parsing columns.

## What I still do not understand

Whether "named by a commit subject on `main`" survives contact with a bead that
lands twice. A follow-up branch for a bead whose earlier completion already
landed will be called landed and vanish from the list — exactly the invisibility
this is meant to end. It has not happened yet, and I did not want to encode a
rule for a case I could not observe. The honest fix is probably to compare the
branch tip's date against the landed commit's, which turns the question from
"has this bead landed" into "has this delivery landed".
