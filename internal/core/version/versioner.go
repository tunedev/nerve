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
	"github.com/tunedev/nerve/internal/core/vfs"
)

// Versioner turns a working tree into versions and back. It is the one thing
// holding all three parts: the tree being versioned, the object store that
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

	tree    vfs.FileSystem
	root    string
	objects *cas.Store
	refs    RefStore
	now     func() time.Time
}

// NewVersioner returns a versioner over the tree beneath root of fsys, keeping
// what it stores in objects and where trees stand in refs.
func NewVersioner(fsys vfs.FileSystem, root string, objects *cas.Store, refs RefStore) *Versioner {
	return &Versioner{
		tree:    fsys,
		root:    path.Join("/", root),
		objects: objects,
		refs:    refs,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Status reports how the working tree differs from what ref stands for. Nothing
// is stored to answer it: a question about a tree should not leave objects
// behind for a sweep to collect.
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

// Commit stores the working tree as a version and moves ref onto it.
//
// A tree that has not changed since ref is not committed again: what ref
// already stands for is returned and no ref is written, so running a commit
// twice leaves one version rather than two saying the same thing.
//
// The move is a compare-and-set against what ref stood for when the walk began,
// so a commit that raced another one fails with ErrConflict rather than
// dropping it.
func (v *Versioner) Commit(ctx context.Context, ref, message string) (ID, error) {
	v.commits.RLock()
	defer v.commits.RUnlock()

	parent, err := v.resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	committed, err := v.entries(ctx, parent)
	if err != nil {
		return "", err
	}
	working, err := v.stored(ctx)
	if err != nil {
		return "", err
	}
	if len(changes(committed, working)) == 0 {
		return parent, nil
	}

	data, err := manifest{Parent: parent, Message: message, Time: v.now(), Entries: working}.encode()
	if err != nil {
		return "", err
	}
	digest, err := v.objects.Put(ctx, data)
	if err != nil {
		return "", fmt.Errorf("commit to %q: %w", ref, err)
	}

	id := ID(digest.String())
	if err := v.refs.Set(ctx, ref, parent, id); err != nil {
		return "", err
	}
	return id, nil
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
// tree. A path the version does not have is fs.ErrNotExist.
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

// Restore makes the working tree hold what id says, and returns what it had to
// change to do it. The zero ID is refused: it names no version, and emptying a
// tree is not what a caller asking to restore one meant.
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

// Reconcile brings the working tree back to what ref stands for, and returns
// what it changed. Divergence is resolved one way: the ref wins, and what comes
// back is what the working tree gave up.
func (v *Versioner) Reconcile(ctx context.Context, ref string) ([]Change, error) {
	id, err := v.refs.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	return v.Restore(ctx, id)
}

// Purge removes every object no version reachable from a ref refers to, and
// returns how many went.
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

// reachable is every object some version behind some ref refers to: the
// manifests themselves and the content they name. A history already walked is
// not walked again, so refs sharing one costs the sweep nothing.
func (v *Versioner) reachable(ctx context.Context) (map[cas.Digest]struct{}, error) {
	names, err := v.refs.List(ctx)
	if err != nil {
		return nil, err
	}

	reachable := make(map[cas.Digest]struct{})
	for _, name := range names {
		id, err := v.resolve(ctx, name)
		if err != nil {
			return nil, err
		}
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

// apply makes one change true in the working tree, reading what the change
// needs from target.
func (v *Versioner) apply(ctx context.Context, change Change, target []entry) error {
	name := path.Join(v.root, change.Path)
	if change.Kind == Removed {
		if err := v.tree.Remove(ctx, name); err != nil {
			return fmt.Errorf("remove %q: %w", change.Path, err)
		}
		return nil
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

	if err := v.tree.Mkdir(ctx, path.Dir(name)); err != nil {
		return fmt.Errorf("write %q: %w", change.Path, err)
	}
	if err := v.tree.Write(ctx, name, data); err != nil {
		return fmt.Errorf("write %q: %w", change.Path, err)
	}
	return nil
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

// hashed names the content of the working tree without storing any of it, which
// is all a comparison needs.
func (v *Versioner) hashed(ctx context.Context) ([]entry, error) {
	var entries []entry
	err := v.walk(ctx, "", func(name string, data []byte) error {
		entries = append(entries, entry{Path: name, Digest: cas.Compute(data).String()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedByPath(entries), nil
}

// stored puts the content of the working tree in the object store, which is
// what has to be true before a manifest can point at it. Content already stored
// is not stored again, so committing a tree that barely changed writes what
// changed and nothing else.
func (v *Versioner) stored(ctx context.Context) ([]entry, error) {
	var entries []entry
	err := v.walk(ctx, "", func(name string, data []byte) error {
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

// walk visits every file beneath the versioned root, with its path relative to
// that root. A root that is not there yet holds no files rather than failing:
// the tree of a version that has never been committed is empty.
func (v *Versioner) walk(ctx context.Context, prefix string, visit func(name string, data []byte) error) error {
	entries, err := v.tree.ReadDir(ctx, path.Join(v.root, prefix))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("walk %q: %w", prefix, err)
	}

	for _, found := range entries {
		name := path.Join(prefix, found.Name)
		if found.IsDir() {
			if err := v.walk(ctx, name, visit); err != nil {
				return err
			}
			continue
		}
		data, err := v.tree.Read(ctx, path.Join(v.root, name))
		if err != nil {
			return fmt.Errorf("read %q: %w", name, err)
		}
		if err := visit(name, data); err != nil {
			return err
		}
	}
	return nil
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
