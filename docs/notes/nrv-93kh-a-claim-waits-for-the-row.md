# nrv-93kh — a claim waits for the row instead of skipping it

`Relay.Carry` publishes first and marks the rows `carried_at` afterwards, so the
worker the notification reached can be claiming while that `UPDATE` still holds
the row. `Postgres.Claim` took the row `FOR UPDATE SKIP LOCKED`, skipped it,
returned `fs.ErrNotExist`, and `Worker.take` treated that as a notification
that outlived its message. The job was gone: marked carried, never claimed, and
only a write triggers a carry.

Reproduced deterministically rather than by looping the end-to-end test: hold
the relay's exact update open in one transaction, then claim from another.

```
before: Claim while the relay marked the row = "", claim "job": file does not exist
after:  ok
```

## Why removing SKIP LOCKED is the fix and not a workaround

`SKIP LOCKED` is there so two consumers take different messages. The claim
addresses a key, and the partial unique index allows at most one pending row
per key, so the second consumer has no other message it could take. All
`SKIP LOCKED` did was make a claim skip a row that *any* writer held: the
relay's `Carried`, a `Record` replacing the body, a `Chmod`. The relay was just
the writer that lost the race most often.

Plain `FOR UPDATE` waits for the holder, then Postgres re-checks
`claimed_at IS NULL` against the committed row. Behind another claim it finds
nothing, so the message is not taken twice
(`TestAClaimBehindAnotherClaimFindsNothing`). Behind any other writer it takes
the message.

The candidate fixes in the bead were rejected:

- Marking carried before publishing trades this loss for another one: a crash
  between the mark and the publish loses the notification.
- A second table for carry bookkeeping takes the lock off the claim's row for
  `Carried` only, not for `Record` or `Chmod`.
- Retrying the claim once treats the symptom and still loses the job whenever
  the lock outlasts the retry.

## What surprised me

The end-to-end test that was meant to show the race is red on every run on
current main, for an unrelated reason: `observed.AsSubber` reports writes
relative to the subtree, so since `resource.Store` began writing through
`Scope.View` the emitter keys jobs `transcript.txt/...`, and the listener,
bound to `resources.#`, never gets them. Filed as nrv-z4gk. An end-to-end check
that fails 100% of the time hides a failure that happens 10% of the time; the
deterministic stack test is the evidence for this fix.

## Still open

- Whether two `Carried` batches from concurrent relays can deadlock each other,
  since each locks several rows. This change neither causes nor fixes that.
