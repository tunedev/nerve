# nrv-v4ar — the one name a plugin cannot know

`FileInfo.Name` is the final element of the path. The mount table translates a
path into the plugin's vocabulary on the way in, so the mount point itself
arrives as `/`, and the plugin answers with the only name that path has:

```
Stat(/files) = name="/" dir=true err=<nil>      memfs mounted at /files
```

Every other path survives the trip, because translation only removes a prefix
and the final element is at the other end. `/files/notes.txt` reaches the plugin
as `/notes.txt` and comes back named `notes.txt` either way. The mount point is
the single path where the two vocabularies disagree about the last segment.

## The fix belongs where `restore` already is

`Readlink` had the same shape and was already handled: the plugin returns
`/jobs/first`, the table returns `/queue/jobs/first`. `restore` does that, and
`restoreName` is its counterpart for the one field of `FileInfo` that carries a
path fragment:

```go
func (m mount) restoreName(rest string, info FileInfo) FileInfo {
	if rest != "/" || m.prefix == "/" {
		return info
	}
	info.Name = path.Base(m.prefix)
	return info
}
```

`rest != "/"` is the whole condition — anything deeper already agrees. The
mount at `/` is excluded because its root really is `/`, which is what
`os.Stat("/")` says too.

## Both verbs that hand a name back, not just the reported one

The bead reported `Stat`. `tableSymlinker.Lstat` is the other verb returning a
`FileInfo` through a translated path and had the identical defect; fixing only
`Stat` would have left a mount point named `files` by one verb and `/` by the
other. `ReadDir` returns children, whose names are already in agreement, and
`Sub` hands out a tree that has left the table, where `/` is the honest answer.

## Why nothing broke before

FUSE never sees it: `READDIRPLUS` carries the name from the parent listing, and
the lookup that follows takes only the mode. It surfaced from `E11.3`, composing
a workspace as one view per area, where every area is a mount point and so every
area was named `/`.

## Tests

`basenamer` in `mount_test.go` — a mount that names an entry by the final
element of what it was handed, which is what a storage plugin does and what
`recorder` deliberately does not, since `recorder` answers a fixed name so a
test can tell mounts apart. `TestStatNamesAMountPointByItsOwnFinalElement`
covers `/`, `/files`, a nested `/files/deep`, and a path inside a mount as the
control; the `Lstat` case is its own test through the symlinker dispatcher.
Both fail on the unfixed table with `name="/"`.
