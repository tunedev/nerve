package version

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tunedev/nerve/internal/core/cas"
)

// Versioner turns a working copy into versions and back. It is the one thing
// holding all three parts: the copy being versioned, the object store that
// holds what a version says, and the refs that say where a tree stands.
//
// A version is content at a set of paths. Directories are walked and not
// recorded, so an empty one does not survive a restore, and modes are not
// recorded either — mode is not a parameter of creation anywhere in the port,
// and most storage kinds have none to record.
//
// Every verb that compares treats an unset ref as the empty version rather than
// as a missing one, because that is what the zero ID already means. Restore is
// the exception: comparing against nothing is a comparison, and restoring to
// nothing would be a deletion of the whole tree wearing a restore's name.
type Versioner struct {
	// commits excludes a sweep, not each other. Two commits race on the ref and
	// the loser is told so, but an object a commit has stored must not be swept
	// between that write and the ref it is named from. Like the local ref
	// store's compare-and-set, that holds for this process and for no other.
	commits sync.RWMutex

	work    WorkingCopy
	objects *cas.Store
	refs    RefStore
	now     func() time.Time
}

// NewVersioner returns a versioner over the working copy work, keeping what it
// stores in objects and where trees stand in refs.
func NewVersioner(work WorkingCopy, objects *cas.Store, refs RefStore) *Versioner {
	return &Versioner{
		work:    work,
		objects: objects,
		refs:    refs,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Status reports how the working copy differs from what ref stands for, which
// is the content and not the position: a copy the ref has moved off says so
// when it is committed from, not when it is asked what changed. Nothing is
// stored to answer it: a question about a tree should not leave objects behind
// for a sweep to collect.
func (v *Versioner) Status(ctx context.Context, ref string) ([]Change, error) {
	id, err := v.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	committed, err := v.entries(ctx, id)
	if err != nil {
		return nil, err
	}
	working, err := v.hashed(ctx)
	if err != nil {
		return nil, err
	}
	return changes(committed, working), nil
}

// Commit stores the working copy as a version and moves ref onto it.
//
// A copy that has not changed since the version it stands on makes no second
// version saying the same thing: that version is what the ref is moved onto and
// what comes back, so running a commit twice leaves one version.
//
// The move is a compare-and-set against where the copy stands, not against a
// fresh reading of the ref. A copy taken from a version the ref has since moved
// off fails with ErrConflict, because committing it would replace a version
// this copy never saw with a tree that was never told about it.
func (v *Versioner) Commit(ctx context.Context, ref, message string) (ID, error) {
	v.commits.RLock()
	defer v.commits.RUnlock()

	stood, err := v.work.Base(ctx)
	if err != nil {
		return "", err
	}
	committed, err := v.entries(ctx, stood)
	if err != nil {
		return "", err
	}
	working, err := v.stored(ctx)
	if err != nil {
		return "", err
	}

	id := stood
	if len(changes(committed, working)) > 0 {
		data, err := manifest{Parent: stood, Message: message, Time: v.now(), Entries: working}.encode()
		if err != nil {
			return "", err
		}
		digest, err := v.objects.Put(ctx, data)
		if err != nil {
			return "", fmt.Errorf("commit to %q: %w", ref, err)
		}
		id = ID(digest.String())
	}

	if err := v.point(ctx, ref, stood, id); err != nil {
		return "", err
	}
	if err := v.work.SetBase(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

// point moves ref off where the copy stood and onto id, and writes nothing when
// it already stands there. A ref standing for nothing is created rather than
// compared: the copy is not taking it off a version, so there is no version for
// the compare-and-set to protect and a copy may be committed onto a new ref.
func (v *Versioner) point(ctx context.Context, ref string, stood, id ID) error {
	current, err := v.resolve(ctx, ref)
	if err != nil {
		return err
	}
	if current == id {
		return nil
	}
	if current == "" {
		stood = ""
	}
	return v.refs.Set(ctx, ref, stood, id)
}

// ListVersions returns the history behind ref, newest first. A ref standing for
// nothing has no history rather than a missing one.
func (v *Versioner) ListVersions(ctx context.Context, ref string) ([]Version, error) {
	id, err := v.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}

	var history []Version
	for id != "" {
		m, err := v.manifest(ctx, id)
		if err != nil {
			return nil, err
		}
		history = append(history, Version{ID: id, Parent: m.Parent, Message: m.Message, Time: m.Time})
		id = m.Parent
	}
	return history, nil
}

// ReadVersion returns the content name held at id, without touching the working
// copy. A path the version does not have is fs.ErrNotExist.
func (v *Versioner) ReadVersion(ctx context.Context, id ID, name string) ([]byte, error) {
	entries, err := v.entries(ctx, id)
	if err != nil {
		return nil, err
	}

	want := relative(name)
	at, found := slices.BinarySearchFunc(entries, want, func(e entry, target string) int {
		return strings.Compare(e.Path, target)
	})
	if !found {
		return nil, fmt.Errorf("read %q at version %s: %w", want, id, fs.ErrNotExist)
	}

	digest, err := cas.ParseDigest(entries[at].Digest)
	if err != nil {
		return nil, fmt.Errorf("read %q at version %s: %w", want, id, err)
	}
	return v.objects.Get(ctx, digest)
}

// Diff reports what would have to happen to the content of from for it to be
// the content of to.
func (v *Versioner) Diff(ctx context.Context, from, to ID) ([]Change, error) {
	before, err := v.entries(ctx, from)
	if err != nil {
		return nil, err
	}
	after, err := v.entries(ctx, to)
	if err != nil {
		return nil, err
	}
	return changes(before, after), nil
}

// Restore makes the working copy hold what id says, and returns what it had to
// change to do it. The zero ID is refused: it names no version, and emptying a
// tree is not what a caller asking to restore one meant.
//
// It changes content and not position. The copy still stands where it stood, so
// committing older content afterwards records a revert onto the version the
// copy was already on rather than colliding with it. Moving the copy is
// Reconcile's, which is the verb about where it stands.
//
// Directories emptied by a restore stay. A version records content at paths and
// nothing about the directories on the way, so removing them would be the
// versioner deciding something it was never told.
func (v *Versioner) Restore(ctx context.Context, id ID) ([]Change, error) {
	if id == "" {
		return nil, fmt.Errorf("restore: the zero version names nothing: %w", fs.ErrInvalid)
	}

	target, err := v.entries(ctx, id)
	if err != nil {
		return nil, err
	}
	working, err := v.hashed(ctx)
	if err != nil {
		return nil, err
	}

	applied := changes(working, target)
	for _, change := range applied {
		if err := v.apply(ctx, change, target); err != nil {
			return nil, fmt.Errorf("restore version %s: %w", id, err)
		}
	}
	return applied, nil
}

// Reconcile brings the working copy back to what ref stands for, and returns
// what it changed. Divergence is resolved one way: the ref wins, and what comes
// back is what the copy gave up.
//
// It resolves both kinds at once. Content the copy holds that the ref does not
// is put back, and the copy is left standing on the ref, so a commit that was
// refused for having been taken from a version the ref moved off now lands on
// top of it. Reconciling to a ref standing for nothing is fs.ErrNotExist rather
// than an emptied tree, which is Restore's refusal of the zero version.
func (v *Versioner) Reconcile(ctx context.Context, ref string) ([]Change, error) {
	id, err := v.refs.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	applied, err := v.Restore(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := v.work.SetBase(ctx, id); err != nil {
		return nil, err
	}
	return applied, nil
}

// Purge removes every object no version reachable from a ref or from where the
// working copy stands refers to, and returns how many went.
//
// It excludes commits for as long as it runs. A commit stores its content
// before the ref that will name it exists, so a sweep running between those two
// would delete content a version is about to claim.
func (v *Versioner) Purge(ctx context.Context) (int, error) {
	v.commits.Lock()
	defer v.commits.Unlock()

	reachable, err := v.reachable(ctx)
	if err != nil {
		return 0, err
	}
	stored, err := v.objects.List(ctx)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, digest := range stored {
		if _, keep := reachable[digest]; keep {
			continue
		}
		if err := v.objects.Remove(ctx, digest); err != nil {
			return removed, fmt.Errorf("purge: %w", err)
		}
		removed++
	}
	return removed, nil
}

// reachable is every object some version behind some root refers to: the
// manifests themselves and the content they name. A history already walked is
// not walked again, so roots sharing one costs the sweep nothing.
func (v *Versioner) reachable(ctx context.Context) (map[cas.Digest]struct{}, error) {
	roots, err := v.roots(ctx)
	if err != nil {
		return nil, err
	}

	reachable := make(map[cas.Digest]struct{})
	for _, id := range roots {
		for id != "" {
			digest, err := cas.ParseDigest(string(id))
			if err != nil {
				return nil, fmt.Errorf("purge: version %q: %w", id, err)
			}
			if _, walked := reachable[digest]; walked {
				break
			}
			reachable[digest] = struct{}{}

			m, err := v.manifest(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, e := range m.Entries {
				content, err := cas.ParseDigest(e.Digest)
				if err != nil {
					return nil, fmt.Errorf("purge: version %s at %q: %w", id, e.Path, err)
				}
				reachable[content] = struct{}{}
			}
			id = m.Parent
		}
	}
	return reachable, nil
}

// roots is every version a sweep must keep: what each ref stands for, and where
// the working copy stands. The copy is a root because it is named from outside
// the refs — a version its ref has moved off is still the one every verb
// comparing against the copy has to read.
func (v *Versioner) roots(ctx context.Context) ([]ID, error) {
	names, err := v.refs.List(ctx)
	if err != nil {
		return nil, err
	}

	roots := make([]ID, 0, len(names)+1)
	for _, name := range names {
		id, err := v.resolve(ctx, name)
		if err != nil {
			return nil, err
		}
		roots = append(roots, id)
	}
	base, err := v.work.Base(ctx)
	if err != nil {
		return nil, err
	}
	return append(roots, base), nil
}

// apply makes one change true in the working copy, reading what the change
// needs from target.
func (v *Versioner) apply(ctx context.Context, change Change, target []entry) error {
	if change.Kind == Removed {
		return v.work.Remove(ctx, change.Path)
	}

	at, found := slices.BinarySearchFunc(target, change.Path, func(e entry, want string) int {
		return strings.Compare(e.Path, want)
	})
	if !found {
		return fmt.Errorf("write %q: %w", change.Path, fs.ErrNotExist)
	}
	digest, err := cas.ParseDigest(target[at].Digest)
	if err != nil {
		return fmt.Errorf("write %q: %w", change.Path, err)
	}
	data, err := v.objects.Get(ctx, digest)
	if err != nil {
		return fmt.Errorf("write %q: %w", change.Path, err)
	}
	return v.work.Write(ctx, change.Path, data)
}

// resolve returns what ref stands for, and the zero ID when it stands for
// nothing. An unset ref is not an error here for the reason it is not one in
// the ref store: the zero ID is what an unset ref resolves to.
func (v *Versioner) resolve(ctx context.Context, ref string) (ID, error) {
	id, err := v.refs.Resolve(ctx, ref)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// manifest returns what id says. The zero ID says nothing, which is what makes
// a comparison against an unset ref a comparison against an empty version.
func (v *Versioner) manifest(ctx context.Context, id ID) (manifest, error) {
	if id == "" {
		return manifest{}, nil
	}
	digest, err := cas.ParseDigest(string(id))
	if err != nil {
		return manifest{}, fmt.Errorf("version %q: %w", id, err)
	}
	data, err := v.objects.Get(ctx, digest)
	if err != nil {
		return manifest{}, fmt.Errorf("read version %s: %w", id, err)
	}
	return decodeManifest(data)
}

// entries is the content a version records, sorted by path.
func (v *Versioner) entries(ctx context.Context, id ID) ([]entry, error) {
	m, err := v.manifest(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.Entries, nil
}

// hashed names the content of the working copy without storing any of it, which
// is all a comparison needs.
func (v *Versioner) hashed(ctx context.Context) ([]entry, error) {
	var entries []entry
	err := v.work.Walk(ctx, func(name string, data []byte) error {
		entries = append(entries, entry{Path: name, Digest: cas.Compute(data).String()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedByPath(entries), nil
}

// stored puts the content of the working copy in the object store, which is
// what has to be true before a manifest can point at it. Content already stored
// is not stored again, so committing a tree that barely changed writes what
// changed and nothing else.
func (v *Versioner) stored(ctx context.Context) ([]entry, error) {
	var entries []entry
	err := v.work.Walk(ctx, func(name string, data []byte) error {
		digest, err := v.objects.Put(ctx, data)
		if err != nil {
			return err
		}
		entries = append(entries, entry{Path: name, Digest: digest.String()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedByPath(entries), nil
}

// sortedByPath puts entries in the order a manifest records them and a diff
// walks them. A sorted walk of a tree does not produce it: "a.txt" sorts before
// "a/b.txt" while the directory is listed first.
func sortedByPath(entries []entry) []entry {
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.Path, b.Path) })
	return entries
}

// relative is how a caller's path is spelled in a version: rooted at the
// versioned root, with no leading separator.
func relative(name string) string {
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}
