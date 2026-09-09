package vfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// MountTable maps prefixes onto plugins and is itself a FileSystem, so a caller
// holds one value for the whole tree and never learns which plugin answered.
//
// The longest prefix holding a path wins: with mounts at "/" and "/queue/jobs",
// "/queue/jobs/next" is the jobs plugin's and "/queue/config" is the root
// plugin's. A prefix names a subtree rather than a string, so "/queue" does not
// hold "/queuebase".
//
// A path arrives in the table's vocabulary and reaches the plugin in the
// plugin's: "/queue/jobs/next" is handed to the mount at "/queue" as
// "/jobs/next", and the mount point itself as "/". Paths a plugin hands back
// make the same trip in reverse, and so does the name of a mount point, which
// the plugin can only call "/" because that is all it was handed.
//
// Being the dispatcher is why the port carries Capabilities and the As*
// accessors instead of leaving callers to type-assert: an assertion would
// interrogate this type, which does no storage work of its own.
//
// Mounts may arrive while the table is being read. The mount list is replaced
// wholesale rather than edited in place, so a reader loads one pointer and
// takes no lock: readers never wait, never contend with each other, and see
// either the tree before a mount or the tree after it. Mount pays for that by
// copying the list, which is what makes mounting the rare operation it already
// is.
type MountTable struct {
	// adding serialises Mount against other Mounts. Readers never take it.
	adding sync.Mutex
	mounts atomic.Pointer[[]mount]
}

// mount is one prefix and the plugin behind it.
type mount struct {
	prefix string
	fsys   FileSystem
}

// NewMountTable returns a table holding nothing. Every path is fs.ErrNotExist
// until something is mounted.
func NewMountTable() *MountTable {
	return &MountTable{}
}

// Mount puts fsys behind prefix, which must be a rooted, clean path such as "/"
// or "/queue". It fails with fs.ErrInvalid on any other prefix and with
// fs.ErrExist on a prefix already taken.
func (t *MountTable) Mount(prefix string, fsys FileSystem) error {
	if !strings.HasPrefix(prefix, "/") || path.Clean(prefix) != prefix {
		return fmt.Errorf("mount %q: prefix is not a rooted clean path: %w", prefix, fs.ErrInvalid)
	}

	t.adding.Lock()
	defer t.adding.Unlock()

	current := t.snapshot()
	if slices.ContainsFunc(current, func(m mount) bool { return m.prefix == prefix }) {
		return fmt.Errorf("mount %q: %w", prefix, fs.ErrExist)
	}

	next := append(slices.Clone(current), mount{prefix: prefix, fsys: fsys})
	slices.SortFunc(next, func(a, b mount) int { return len(b.prefix) - len(a.prefix) })
	t.mounts.Store(&next)
	return nil
}

// snapshot is the mount list as it stands, safe to read without a lock because
// nothing ever writes into a list that has been stored.
func (t *MountTable) snapshot() []mount {
	if current := t.mounts.Load(); current != nil {
		return *current
	}
	return nil
}

// resolve finds the deepest mount holding path and translates path into that
// mount's vocabulary.
func (t *MountTable) resolve(name string) (mount, string, error) {
	name = rooted(name)
	for _, m := range t.snapshot() {
		if rest, ok := m.holds(name); ok {
			return m, rest, nil
		}
	}
	return mount{}, "", fmt.Errorf("resolve %q: no mount holds it: %w", name, fs.ErrNotExist)
}

// rooted is path.Clean over a path that may arrive without its leading slash.
//
// The already-rooted case is spelled out rather than folded into one
// path.Clean("/" + name), because nearly every path arrives rooted and adding
// a second slash to it makes "//queue/jobs", which path.Clean then has to
// rebuild a string to repair: three allocations on every verb, to undo work
// nothing asked for. Testing the first byte costs a compare, and a rooted clean
// path comes back out of path.Clean as itself.
func rooted(name string) string {
	if strings.HasPrefix(name, "/") {
		return path.Clean(name)
	}
	return path.Clean("/" + name)
}

// holds reports whether the mount owns name, and returns name in the mount's
// own vocabulary. name is already lexically resolved.
func (m mount) holds(name string) (string, bool) {
	if m.prefix == "/" {
		return name, true
	}
	if !strings.HasPrefix(name, m.prefix) {
		return "", false
	}
	switch rest := name[len(m.prefix):]; {
	case rest == "":
		return "/", true
	case rest[0] == '/':
		return rest, true
	default:
		return "", false
	}
}

// restore puts a path the plugin returned back into the table's vocabulary.
func (m mount) restore(name string) string {
	if m.prefix == "/" {
		return name
	}
	return m.prefix + name
}

// restoreName puts the name of an entry the plugin described back into the
// table's vocabulary. Only the mount point differs: the plugin was handed its
// own root and named it "/", where the table knows the entry by the final
// element of the prefix. rest is the path in the plugin's vocabulary.
func (m mount) restoreName(rest string, info FileInfo) FileInfo {
	if rest != "/" || m.prefix == "/" {
		return info
	}
	info.Name = path.Base(m.prefix)
	return info
}

// pair resolves two paths that one plugin has to handle together. It fails with
// fs.ErrInvalid when they land on different mounts, because no plugin can act
// on an entry it does not hold.
func (t *MountTable) pair(verb, oldPath, newPath string) (mount, string, string, error) {
	from, oldRest, err := t.resolve(oldPath)
	if err != nil {
		return mount{}, "", "", err
	}
	to, newRest, err := t.resolve(newPath)
	if err != nil {
		return mount{}, "", "", err
	}
	if from.prefix != to.prefix {
		return mount{}, "", "", fmt.Errorf("%s %q to %q across mounts: %w", verb, oldPath, newPath, fs.ErrInvalid)
	}
	return from, oldRest, newRest, nil
}

// The ten verbs all do the same thing: resolve the path, hand it on. A mount
// table stores nothing itself.
func (t *MountTable) Create(ctx context.Context, name string) error {
	m, rest, err := t.resolve(name)
	if err != nil {
		return err
	}
	return m.fsys.Create(ctx, rest)
}

func (t *MountTable) Mkdir(ctx context.Context, name string) error {
	m, rest, err := t.resolve(name)
	if err != nil {
		return err
	}
	return m.fsys.Mkdir(ctx, rest)
}

func (t *MountTable) Remove(ctx context.Context, name string) error {
	m, rest, err := t.resolve(name)
	if err != nil {
		return err
	}
	return m.fsys.Remove(ctx, rest)
}

func (t *MountTable) RemoveAll(ctx context.Context, name string) error {
	m, rest, err := t.resolve(name)
	if err != nil {
		return err
	}
	return m.fsys.RemoveAll(ctx, rest)
}

func (t *MountTable) Read(ctx context.Context, name string) ([]byte, error) {
	m, rest, err := t.resolve(name)
	if err != nil {
		return nil, err
	}
	return m.fsys.Read(ctx, rest)
}

func (t *MountTable) Write(ctx context.Context, name string, data []byte) error {
	m, rest, err := t.resolve(name)
	if err != nil {
		return err
	}
	return m.fsys.Write(ctx, rest, data)
}

func (t *MountTable) ReadDir(ctx context.Context, name string) ([]FileInfo, error) {
	m, rest, err := t.resolve(name)
	if err != nil {
		return nil, err
	}
	return m.fsys.ReadDir(ctx, rest)
}

func (t *MountTable) Stat(ctx context.Context, name string) (FileInfo, error) {
	m, rest, err := t.resolve(name)
	if err != nil {
		return FileInfo{}, err
	}
	found, err := m.fsys.Stat(ctx, rest)
	if err != nil {
		return FileInfo{}, err
	}
	return m.restoreName(rest, found), nil
}

func (t *MountTable) Rename(ctx context.Context, oldPath, newPath string) error {
	m, oldRest, newRest, err := t.pair("rename", oldPath, newPath)
	if err != nil {
		return err
	}
	return m.fsys.Rename(ctx, oldRest, newRest)
}

func (t *MountTable) Chmod(ctx context.Context, name string, mode fs.FileMode) error {
	m, rest, err := t.resolve(name)
	if err != nil {
		return err
	}
	return m.fsys.Chmod(ctx, rest, mode)
}

// Capabilities asks the plugin holding name what it is. A path no mount holds
// claims nothing: the zero value, and every verb there fails with
// fs.ErrNotExist anyway.
func (t *MountTable) Capabilities(name string) Caps {
	m, rest, err := t.resolve(name)
	if err != nil {
		return Caps{}
	}
	return m.fsys.Capabilities(rest)
}

// AsSymlinker returns a dispatcher over the mounts that have link verbs, or nil
// if none has.
func (t *MountTable) AsSymlinker() Symlinker {
	if !t.anyMountAnswers(func(f FileSystem) bool { return f.AsSymlinker() != nil }) {
		return nil
	}
	return tableSymlinker{table: t}
}

// AsStreamer returns a dispatcher over the mounts that have streaming verbs, or
// nil if none has.
func (t *MountTable) AsStreamer() Streamer {
	if !t.anyMountAnswers(func(f FileSystem) bool { return f.AsStreamer() != nil }) {
		return nil
	}
	return tableStreamer{table: t}
}

// AsTruncater returns a dispatcher over the mounts that have the truncate verb,
// or nil if none has.
func (t *MountTable) AsTruncater() Truncater {
	if !t.anyMountAnswers(func(f FileSystem) bool { return f.AsTruncater() != nil }) {
		return nil
	}
	return tableTruncater{table: t}
}

// AsSubber returns a dispatcher over the mounts that hand out subtrees, or nil
// if none does.
func (t *MountTable) AsSubber() Subber {
	if !t.anyMountAnswers(func(f FileSystem) bool { return f.AsSubber() != nil }) {
		return nil
	}
	return tableSubber{table: t}
}

// anyMountAnswers reports whether some mounted plugin has the verb set.
//
// It is what keeps the caller's `if s := fsys.AsStreamer(); s != nil` guard
// worth writing: a table of plugins that all refuse the verbs refuses them too.
// The guard cannot be more precise than that, because an accessor takes no
// path; a mount that refuses the verbs inside a table where others answer says
// so per path, as fs.ErrInvalid.
func (t *MountTable) anyMountAnswers(has func(FileSystem) bool) bool {
	return slices.ContainsFunc(t.snapshot(), func(m mount) bool { return has(m.fsys) })
}

type tableSubber struct {
	table *MountTable
}

// Sub is the subtree the mount holding name hands out, in that mount's own
// vocabulary. It is the one accessor verb that answers per path without an
// error to say so: a mount with no subtree to give is not a failure but a tree
// whose containment is already the name it was handed, and nil says exactly
// that.
//
// What comes back has left the table. A subtree is a root of its own, so paths
// under it are the subtree's and no longer any prefix's.
func (s tableSubber) Sub(name string) FileSystem {
	m, rest, err := s.table.resolve(name)
	if err != nil {
		return nil
	}
	sub := m.fsys.AsSubber()
	if sub == nil {
		return nil
	}
	return sub.Sub(rest)
}

type tableSymlinker struct {
	table *MountTable
}

func (s tableSymlinker) Symlink(ctx context.Context, oldPath, newPath string) error {
	m, oldRest, newRest, err := s.table.pair("symlink", oldPath, newPath)
	if err != nil {
		return err
	}
	link := m.fsys.AsSymlinker()
	if link == nil {
		return fmt.Errorf("symlink %q: mount %q has no link verbs: %w", oldPath, m.prefix, fs.ErrInvalid)
	}
	return link.Symlink(ctx, oldRest, newRest)
}

func (s tableSymlinker) Readlink(ctx context.Context, name string) (string, error) {
	m, rest, err := s.table.resolve(name)
	if err != nil {
		return "", err
	}
	link := m.fsys.AsSymlinker()
	if link == nil {
		return "", fmt.Errorf("readlink %q: mount %q has no link verbs: %w", name, m.prefix, fs.ErrInvalid)
	}

	target, err := link.Readlink(ctx, rest)
	if err != nil {
		return "", err
	}
	return m.restore(target), nil
}

// Lstat describes name as the mount holding it does, without following a link.
// A mount with no link verbs answers with its own Stat rather than a refusal,
// because a kind with no links cannot have one at name — which is what the
// package Lstat says, and this is a table of mounts that answer it differently.
func (s tableSymlinker) Lstat(ctx context.Context, name string) (FileInfo, error) {
	m, rest, err := s.table.resolve(name)
	if err != nil {
		return FileInfo{}, err
	}
	found, err := Lstat(ctx, m.fsys, rest)
	if err != nil {
		return FileInfo{}, err
	}
	return m.restoreName(rest, found), nil
}

type tableStreamer struct {
	table *MountTable
}

func (s tableStreamer) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	m, rest, err := s.table.resolve(name)
	if err != nil {
		return nil, err
	}
	stream := m.fsys.AsStreamer()
	if stream == nil {
		return nil, fmt.Errorf("open %q: mount %q has no streaming verbs: %w", name, m.prefix, fs.ErrInvalid)
	}
	return stream.OpenRead(ctx, rest)
}

func (s tableStreamer) OpenWrite(ctx context.Context, name string) (io.WriteCloser, error) {
	m, rest, err := s.table.resolve(name)
	if err != nil {
		return nil, err
	}
	stream := m.fsys.AsStreamer()
	if stream == nil {
		return nil, fmt.Errorf("open %q: mount %q has no streaming verbs: %w", name, m.prefix, fs.ErrInvalid)
	}
	return stream.OpenWrite(ctx, rest)
}

type tableTruncater struct {
	table *MountTable
}

func (c tableTruncater) Truncate(ctx context.Context, name string, size int64) error {
	m, rest, err := c.table.resolve(name)
	if err != nil {
		return err
	}
	cut := m.fsys.AsTruncater()
	if cut == nil {
		return fmt.Errorf("truncate %q: mount %q has no truncate verb: %w", name, m.prefix, fs.ErrInvalid)
	}
	return cut.Truncate(ctx, rest, size)
}
