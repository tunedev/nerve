# nrv-amti — Two roots and a secret

`cmd/nerved` now builds `resource.Store` and `kv.Space`. Three settings arrived:
`resources-root` and `scratch-root` name where each area of the tree begins, and
`download-key-file` names the file the download signing key is read out of. All
three are empty by default, so a checkout with no resources mount boots exactly
as it did.

## What the pattern was

The roots are `library.Spec.Root` spelled for one area instead of a list: a
rooted clean path, the mount table's rule for a prefix, checked at boot beside
every other setting. Both stores refuse anything else where they are built, so
the check in `config.Validate` adds no rule — it moves an existing one to where
an operator reads the whole list at once.

The secret is the part that is not config. The document names the file; the
composition root reads it. That keeps the key out of every layer that resolves a
setting, which is what keeps it out of the boot banner and off `/config`
without anything having to remember to redact it.

`resources`, `scratch`, `documents`, `store` — the composition root now holds
four things a mount or a store is built over, all for the same reason: what two
callers share is owned where the process is assembled.

## What surprised me

**The first rule that spans two settings.** `Validate` is a `errors.Join` of
per-field checks, and every one of them reads one value. `resources-root` with
no `download-key-file` reads two. It went in as `signable(root, keyFile)` beside
the rest rather than as a check on either field, because the failure belongs to
neither: a root is fine, an unnamed key file is fine, and the pair is not.

**`bytes.TrimSpace` was the wrong trim.** A key written with `echo` carries a
newline nobody meant to sign with, so something has to go. `TrimSpace` also eats
leading bytes and any interior-adjacent whitespace, which silently changes a key
that is not text. `TrimRight(key, "\r\n")` takes the shell's newline and nothing
else, and the file's contract is one line.

**Nothing consumes either store yet.** The boot banner is the only reader, which
is where `library.Catalog` also stands. `ingest.New` and `content.NewReader`
already take a `*resource.Store` and nrv-4jwb is the bead that runs them; the
inbound surface is E9.6. This increment makes the store exist in a process, and
that was the whole of what was missing.

**A false green was one line away.** A key made up at boot passes any test that
signs and redeems through one store. The case that catches it builds a second
store from the same file and redeems the first one's token, and it fails as
`permission denied` the moment the key stops coming from the file:

```
$ go test ./cmd/nerved/ -count=1     # with the key generated per process
--- FAIL: TestAGrantOutlivesTheProcessThatSignedIt (0.00s)
    roots_test.go:154: Download: download: verify token: this signer did not
    sign it: permission denied
--- FAIL: TestATrailingNewlineIsNotPartOfTheKey (0.00s)
    roots_test.go:207: Download: download: verify token: this signer did not
    sign it: permission denied
```

## What I would do differently

Nothing in the shape. The one thing worth stating rather than assuming: a
`download-key` setting alongside the rest would have worked and been wrong, and
the argument against it is not that a setting is unsafe — it is that every layer
able to set it is a layer able to echo it, and the surfaces that echo settings
are exactly the ones an operator points at a screen.

## What I still do not understand

**Whether the key should be re-readable without a restart.** Tenet 6 asks which
changes are hot and which are structural, and this one is neither cleanly: the
signer holds the bytes, so rotation today is a restart, and grants signed with
the old key stop being redeemable at that moment rather than at their expiry.
Holding two keys — sign with the new, verify against both — is what a rotation
without a gap needs, and nothing has asked for one yet.

**Whether a root should have to name a mount.** It does not, and that is
deliberate for a library: a mount may arrive after the table is read. A resource
store over an unmounted root is the same promise, but it fails at the first
upload rather than at boot, and an operator who misspelt the root learns it from
a user.

**Whether empty should stay a real answer.** It is right now — no root, no
store, and the process boots. Once an inbound surface serves uploads, a build
with no resources root is a build whose upload routes cannot answer, and whether
that is a boot failure or a route that reports there is nowhere to put anything
is E9.6's question, not this one's.
