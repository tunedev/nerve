# nrv-9b3 — bounding a transfer by idleness

`WithTimeout` gave the two streaming opens a deadline and left what they
returned running on the caller's context. A transfer that stopped moving bytes
was therefore bounded by nothing: the open had answered, so the bound was
lifted, and a plugin waiting on a far end that had gone quiet waited forever.

`internal/core/vfs/idle.go` adds the second bound. `idling` arms a
`time.AfterFunc` over the transfer's context, every read or write that moves a
byte resets it, and closing the stream stops it. Nothing moved for the bound
cancels the transfer, which is the only handle anything outside the plugin has
on a read already in flight.

## What the pattern was

**A transfer has no length worth bounding.** A healthy download is as long as
the entry, so a duration limit on the whole transfer is a limit on file size
wearing a clock. What can be bounded is the gap: no byte crossing for `d`.

**The duration is the call bound, reused.** `call-timeout` is the patience an
operator granted one call to a mount, and a quiet period in a transfer is the
same patience spent the same way. A second setting would let the two differ,
and nothing yet needs them to.

**Cancelling is telling, not stopping.** Ending the transfer's context is a
request the plugin honours; a plugin inside a blocking syscall stays there.
Two guards make the bound hold anyway: a read that returns after the transfer
ended reports the stall rather than the cancellation the plugin saw, and the
next read on a stalled stream fails before reaching the plugin at all.

**`Close` reports it.** A streamed write is complete exactly when `Close`
returns nil, so a transfer the bound ended must not close as though it landed.

## What surprised me

**The test that had to change was the one that stated the old promise.**
`TestAStreamOutlivesTheBoundOnItsOpen` slept five times the bound with the
stream untouched and asserted the transfer was still running. That is the
failure this ticket exists to fix, spelled as an assertion. The property worth
keeping is the same sentence with two words added: a stream outlives the bound
on its open *while it moves*, so the test now reads a byte at a time with gaps
shorter than the bound and longer than it in total.

**A stalled read is not distinguishable from a slow one without reading.** The
bound is measured across calls the caller makes, so a caller that stops asking
is a caller that stalls its own transfer. That is the right reading — an idle
stream holds a connection open either way — but it means the bound belongs to
the pair, not to the far end.

## What I chose not to build

A separate `idle-timeout` setting. It is one line of config and three of
plumbing, and until a mount exists whose transfers want more patience than its
calls, the value it would carry is a guess.

Interrupting a plugin that ignores its context. The only way is to run the read
on a goroutine and abandon it, which trades a hung caller for a leaked
goroutine and a stream nobody owns.

## What I still do not understand

Whether the bound should be per direction. A read stalls when the far end goes
quiet; a write stalls when the caller does. They are the same duration here
because they are the same setting, but they are not the same event, and an
operator who wants to be patient with a slow uploader and strict with a silent
downloader cannot say so.
