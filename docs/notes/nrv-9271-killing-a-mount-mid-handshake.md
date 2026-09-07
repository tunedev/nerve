# nrv-9271 — a mount that is in the table is not yet safe to kill

`TestAKilledProcessLeavesNoMountBehind` re-executes the test binary as a helper
that mounts and blocks, kills it, and asserts on the corpse it leaves. Under a
full-module `go test -race` the case sometimes parked in `cmd.Wait` until the
package timed out at 600s.

The helper was declared ready when its mount appeared in `/proc/self/mountinfo`.
It now writes a file after `fusefs.Serve` returns, and the parent waits for that.
`kill` also bounds its reap, so the failure that remains is named rather than a
package timeout.

## What the pattern was

The mount entry appears when `fusermount3` finishes `mount(2)`, which is early.
`fs.Mount` has not returned at that point: go-fuse ends the mount with
`Server.WaitMount`, and `WaitMount` ends with `pollHack`, which opens
`<mountpoint>/.go-fuse-epoll-hack` — a real FUSE request through the mount this
same process serves, made so the server can answer `ENOSYS` to `_OP_POLL` once.

SIGKILL landing in that window kills the thread that would answer while another
thread is parked in the kernel waiting for the answer. A FUSE request already
handed to userspace is an uninterruptible wait. The wait ends when the
connection aborts, the connection aborts when the `/dev/fuse` descriptor closes,
and the descriptor closes when the last thread exits — which is the thread that
is waiting. Nothing breaks the cycle, so the process is never fully reaped and
`waitid` on it never returns.

Readiness was the wrong predicate, not the wait. `Serve` returning means the
handshake is over and no request of the helper's own is in flight.

## What surprised me

**The hang leaves a process nothing can kill.** Six were on the machine when I
reproduced it — three older than this session. `SIGKILL` does not touch them and
`kill -9` on the parent does not either; only writing to
`/sys/fs/fuse/connections/<minor>/abort` releases the parked thread. Each one
also holds its mount and its `t.TempDir`. That is the second symptom, and the
ticket names only the timeout.

**It reproduces on demand once you stop running the whole module.** Twenty-eight
busy loops on a 32-core box and the single case in a loop hung on iteration 33,
about a minute in. The load matters because it widens the window between the
mount appearing and `Serve` returning; the full-module run was never the
ingredient, only the load it happens to create.

## What I would do differently

Read the readiness predicate first. The mount table is what the case asserts on
afterwards, and reusing it as the start signal reads as economy right up to the
point where you ask what the mount being listed actually proves.

## What I still do not understand

Whether the same window is reachable from outside a test. Anything that kills a
`nerved` between `fusermount3` and `Serve` returning gets the same unkillable
process, and nothing in the package narrows that window — the fix here is the
test learning to wait, not the mount learning to be killed. A shutdown path that
aborts its own connection before exiting would close it, and whether that is
`nerved`'s business or the supervisor's is the same question `reclaim` raised.

## Evidence

Reproduction, before the fix — the single case in a loop under 28 busy loops:

```
$ N=120 LOAD=28 ./repro.sh
HUNG on iteration 33

=== pid 2219656 state ===
State:	Z (zombie)
Threads:	2
--- thread 2219656 wchan: 0                    stat: Z
--- thread 2219663 wchan: request_wait_answer  stat: D
```

with the parent exactly as the ticket reported:

```
goroutine 21 [syscall]:
syscall.Syscall6(0xf7, ...)
internal/syscall/unix.Waitid(...)
os.(*Process).pidfdWait(...)
os/exec.(*Cmd).Wait(...)
fusefs_test.kill(...)
	lifecycle_test.go:176
fusefs_test.TestAKilledProcessLeavesNoMountBehind(...)
	lifecycle_test.go:52
```

After the fix, the same loop and the same load:

```
$ N=300 LOAD=28 ./repro.sh
no hang in 300 iterations
```

```
$ go test -race -count=1 ./internal/adapters/inbound/fusefs/
ok  	github.com/tunedev/nerve/internal/adapters/inbound/fusefs	1.545s

$ go test -race -count=1 ./...    # rc=0, 59 packages ok
$ go vet ./...                    # rc=0
$ make fmt-check                  # rc=0
$ make lint
go tool golangci-lint config verify
go tool golangci-lint run
0 issues.
```
