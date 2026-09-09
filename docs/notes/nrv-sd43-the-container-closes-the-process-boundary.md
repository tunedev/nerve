# nrv-sd43 — a container closes the process boundary, the mount closes data

E11.2 left two items on the escape surface that nothing under E11 could close:
the kernel resolves `..` against its own dentry tree before a filesystem is
asked, and the mount point is an ordinary host path. E11.5 added a third — a
FUSE mount outlives the process serving it, and `reclaim` bounds that window
rather than closing it. All three want a process boundary, and a filesystem has
no verb for one.

The decision: **a Docker container closes the process boundary; the scoped VFS
served by `fusefs` closes the filesystem boundary.** The container belongs to
whatever runs user code, which is atlas, not to E11. E11 owns the data boundary
and the contract the runner has to satisfy for the two to compose.

**None of this is measured.** The evidence comes from the atlas sandbox being
built in parallel, and that sandbox does not exist yet. Everything below is
reasoning from what E11.2, E11.4 and E11.5 *did* measure plus what
`fusefs.Serve` demonstrably sets; the probes that would turn it into evidence
are named at the end, and until they run this note is a decision and not a
result. Sandboxing user-authored code is a correctness requirement under the
Forge constraints, so a note that read as settled would be worse than no note.

## Why a container and not a bare mount namespace

The two open items ask for `pivot_root` plus a private mount namespace. A
container is that, with a lifecycle, a cgroup and a filesystem image attached,
run by something that already exists. Docker is a standing Forge development
constraint — `kind` runs on it — so this adds no dependency and passes the plane
test unchanged.

E11.6 refuses 9P and the gVisor gofer as fleet-scale Kubernetes machinery, and
that refusal stands untouched: it is about the transport Nerve serves its data
over, not about what runs a process. Choosing `runsc` as a container runtime
later would not reopen it.

## A container does not stop `..`. It changes what `..` reaches

This is the part most likely to be written down wrong. Inside a container the
workspace mount is a subtree of the root, not the root. `cat
/workspace/sub/../../etc/passwd` still climbs out of the mount, still generates
zero `LOOKUP` calls at the filesystem, and still succeeds — it reads
`/etc/passwd` *of the container image*, which belongs to nobody and contains
nothing of anyone's.

So the property a container buys is not "the climb is refused". It is
**everything above the mount belongs to no one**. That is weaker than the
literal requirement in this ticket's own description — that E11.4's cases run
from a process whose root *is* the mount — and it is sufficient, because what
the requirement was protecting was the host tree beside the mount point, and a
container does not contain that tree.

E11.4 already models the stronger property with `os.Root`, which resolves under
`RESOLVE_IN_ROOT` and gives a caller the resolution rules `pivot_root` would.
The two assertions are different and both are wanted: `os.Root` asserts that
Nerve refuses a climb when the mount is the root; the container asserts that a
climb which is not Nerve's to refuse lands somewhere harmless.

## What a container does not close

**Cross-scope leakage inside the filesystem.** A process boundary says nothing
about whether alice's view can reach bob's subtree. `nrv-aeyc` and `nrv-d42y`
closed that at the source, and if either regressed, every container in the world
would serve the leak faithfully. This is the correctness requirement, and the
container is not what holds it.

**The symlink Nerve emits.** E11.2 item 1 — the one escape the filesystem
authors rather than resolves. A container downgrades an absolute target from
"reads the host" to "reads the image", which is a severity change and not a
closure; a target pointing at another scope is unaffected. `fusefs` serves no
link verbs at all, and that is what refuses it.

**The orphaned mount.** This one inverts an argument E11.5 made. The mount is
served by `nerved` in `nerved`'s mount namespace and bound into the container;
it does not live in the container's namespace and therefore does not die with
it. Killing the container reclaims nothing. E11.5 reads a private mount
namespace as closing the window outright and calls it the same machinery this
ticket is deciding about; that transfer does not hold: `reclaim` inside `Serve`
stays load-bearing, and the window it bounds stays open. It would only close if
`nerved` itself served each mount from a namespace bounded by the workspace's
life, which is a different change and nobody's ticket.

**A handle across a revocation.** E11.2 item 5. A descriptor already handed out
is closed by closing it, in any namespace.

**The kernel.** The container shares it. A local privilege escalation from
inside is out, and no arrangement of mounts changes that. This is the ceiling on
the decision and the thing a kernel-boundary runtime would raise.

**Whatever the container is given.** Default Docker is permissive where it
matters here: full network egress on the default bridge, no memory limit, a
writable layer. Egress from a process holding another person's CV is a data
boundary too. Named because "it runs in a container" is routinely read as
covering it.

**The host path itself.** E11.2 item 4 is narrowed, not deleted. The mount point
is still an ordinary path on the host, and whatever can read it is still inside
the workspace — the set is now "processes on the host" instead of "the
sandboxed process", which is progress and not closure. The mount point's parents
have to be owned by `nerved`'s uid and unreadable by anyone else. Root is inside
regardless.

## The uid collision, which is the first thing that will fail

`fusefs.Serve` never sets `AllowOther`, deliberately, and the kernel records
`user_id`/`group_id` from the mounting process. E11.2 measured
`user_id=1000,group_id=1000` on a live mount. The kernel therefore refuses
access from any other uid before a request reaches the filesystem.

The container's instinct is the opposite: run user code as some uid that is not
the daemon's, and better still remap it through a user namespace so it is not a
host uid at all. Those two settings are incompatible. Three ways out, and the
choice matters:

- **Run the sandboxed process as `nerved`'s uid** (`--user $(id -u)`, no userns
  remapping). The mount works with no widening. The cost is that a process that
  escapes the container holds the daemon's uid on the host.
- **Set `AllowOther`.** Every uid on the host is then inside the mount, and it
  additionally needs `user_allow_other` in `/etc/fuse.conf`, which E11.2
  measured as commented out on the development host. This hands item 4 back and
  makes the mount point's directory mode the only thing left holding it.
- **Serve the mount inside the container.** Needs `/dev/fuse` and either
  `CAP_SYS_ADMIN` or the setuid helper, which gives back much of what the
  container was for, and puts a Nerve daemon inside the sandbox it is
  containing.

The first is the default this note recommends. It is a real cost, stated rather
than hidden: the process boundary here is a namespace boundary, not a uid
boundary.

## The contract E11 hands to the runner

Written as a list because it is the thing that will be implemented against.

1. The mount exists before the container starts. Docker binds a host path at
   start under `rprivate` propagation, so a mount made afterwards is not visible
   inside — believed, not measured, and probe C below is the one that says.
2. The bind target inside the container is the only path to workspace data.
   Everything else in the tree is the image.
3. The sandboxed process's uid can read the mount, by the first option above.
4. No `/dev/fuse`, no `CAP_SYS_ADMIN`, no Docker socket.
5. Network egress is denied unless the workload was given it on purpose.
6. Container teardown does not unmount anything. `nerved` unmounts, and
   `reclaim` covers the case where `nerved` died first.

## Not proven, and what would prove it

Each probe with the assertion that makes it evidence rather than a demo. They
belong where the sandbox is built.

- **A. The climb.** From inside the container, read
  `/workspace/sub/../../etc/passwd`. Assert it reads the *image's* file, and
  that the filesystem logged zero `LOOKUP`s for any component above the mount.
  A count of calls that did not happen only reads as evidence beside non-zero
  counts from the same run, as in E11.2.
- **B. The host path.** Assert the host mount-point spelling appears nowhere in
  the container's `/proc/self/mountinfo`, and that no path inside reaches it
  except the bind target.
- **C. Propagation and ordering.** Mount after the container starts; assert the
  container sees nothing there. Then assert the mount-first ordering works. This
  is the claim in contract item 1 and it is the one I am least sure of.
- **D. The orphan.** `docker kill` the container, then count entries for the
  mount point in the host's `/proc/self/mountinfo`. Assert the mount is still
  there. This asserts the *negative* claim above — that the container reclaims
  nothing — and it is the assertion that keeps `reclaim` from being deleted as
  redundant.
- **E. The dead daemon.** SIGKILL `nerved` while the container reads. Assert
  reads fail with `ENOTCONN` promptly rather than hanging, matching E11.5's
  host-side measurement from inside.
- **F. The uid.** Run the container as a uid that is not the daemon's. Assert
  the mount is unreadable, and that the failure is `EACCES` at the kernel with
  no request reaching the filesystem. Then assert the recommended option works.
- **G. Cross-scope, from inside.** Run E11.4's planted-link cases from inside
  the container. Assert they fail exactly as they do at the port — the container
  must change nothing here, and a difference means the containment moved.
- **H. Privilege.** From inside, assert `mount` fails, `/dev/fuse` is absent,
  and no Docker socket is reachable.

Until A–H exist, the correct summary of this decision is: chosen, reasoned,
unmeasured.

## What would reopen it

- User code that must be run against an untrusted kernel surface — then a
  kernel-boundary runtime (`runsc`, or a microVM) replaces the runtime under the
  same contract, and item 4 of the contract is what makes that a swap rather
  than a rewrite. E11.6 is not in the way.
- The hosted prod stack. The Forge constraint puts prod on a cloud free tier
  running the app and Postgres, and a free tier may grant neither `/dev/fuse`
  nor nested containers. This note decides the laptop stack; it says nothing
  about what closes the boundary in prod, and that gap is real rather than
  deferred by choice.
- A measured need for `AllowOther` — for instance if the runner cannot be made
  to share the daemon's uid — which changes the exposure enough to be a
  different decision.

## What surprised me

**E11.5's argument for a namespace does not survive the answer.** The note reads
as though whatever closes the process boundary also closes the orphan window,
because both are "a private mount namespace". They are different namespaces:
the one the *consumer* runs in, and the one the *server* mounts in. A container
gives the first and the orphan lives in the second. The chain of notes had this
pointing the same way for three tickets and the direction only shows up when
you name which process holds the namespace.

**The uid check reads as a boundary from the daemon's side and as an outage
from the container's.** Every line about `AllowOther` in this repo is written as
"never widen it", which is right, and none of them notices that the same setting
is what lets a differently-owned process in at all. It is the one place where
the two boundaries are not merely independent but pull against each other.

## What I still do not understand

**Whether one container gets one mount or one per area.** E11.5 left this open
for `nerved` and a container makes it sharper: four mounts is four bind entries,
four reclaims and four ways for the ordering in contract item 1 to be wrong.

**Where the sandbox's own tests live.** G asserts a Nerve property from inside a
container built by atlas. That is Nerve's assertion running in another repo's
harness, and neither repo has a place for it today.

**Whether `nerved` should refuse to serve a mount whose parent directory is
readable by anyone else.** It is checkable at `Serve` in three lines and it
turns contract item 6's sibling — the mount point's mode — from a runbook line
into a refusal. It is also a filesystem daemon making a policy call about host
layout, which may be the wrong altitude.
