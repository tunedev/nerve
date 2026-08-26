package vfs

import (
	"fmt"
	"hash/maphash"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The three ways a mount table can survive concurrent readers, measured against
// each other, plus the shipped table itself. E12.11 asks whether partitioning
// the key space reduces contention on the one structure every verb touches;
// this is the measurement that answers it, and it is what the answer has to be
// re-argued against.
//
// This is the one test file inside package vfs rather than vfs_test. The
// strategies have to do identical work to be comparable at all, and that work
// is rooted and mount.holds. Reaching them from outside would mean either
// copying them, which measures the copy, or going through the ten verbs, which
// measures plugin dispatch as well.

// rwmutexTable guards the whole slice with one lock: the obvious way to make a
// racy read path safe, and the baseline the other two must beat.
type rwmutexTable struct {
	mu     sync.RWMutex
	mounts []mount
}

func (t *rwmutexTable) mount(prefix string, fsys FileSystem) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mounts = append(t.mounts, mount{prefix: prefix, fsys: fsys})
	slices.SortFunc(t.mounts, func(a, b mount) int { return len(b.prefix) - len(a.prefix) })
}

func (t *rwmutexTable) resolve(name string) (mount, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return scan(t.mounts, name)
}

// shardedTable is the partitioning under test: mounts are split by the first
// path segment, each shard behind its own lock, so two goroutines resolving
// under different segments never touch the same lock.
//
// A mount at "/" holds every path, so it lands in every shard. That is the
// first thing the partitioning costs.
type shardedTable struct {
	seed   maphash.Seed
	shards []rwmutexTable
}

func newShardedTable(n int) *shardedTable {
	return &shardedTable{seed: maphash.MakeSeed(), shards: make([]rwmutexTable, n)}
}

func (t *shardedTable) shardOf(name string) *rwmutexTable {
	return &t.shards[maphash.String(t.seed, segment(name))%uint64(len(t.shards))]
}

func (t *shardedTable) mount(prefix string, fsys FileSystem) {
	if prefix == "/" {
		for i := range t.shards {
			t.shards[i].mount(prefix, fsys)
		}
		return
	}
	t.shardOf(prefix).mount(prefix, fsys)
}

func (t *shardedTable) resolve(name string) (mount, bool) {
	return t.shardOf(name).resolve(name)
}

// segment is the partition key: the first component of a rooted path, so
// "/queue/jobs/next" and "/queue/config" land in one shard.
func segment(name string) string {
	rest := strings.TrimPrefix(name, "/")
	if cut := strings.IndexByte(rest, '/'); cut >= 0 {
		return rest[:cut]
	}
	return rest
}

// snapshotTable replaces the slice wholesale on every mount, so a reader loads
// a pointer and takes no lock at all.
type snapshotTable struct {
	mu     sync.Mutex
	mounts atomic.Pointer[[]mount]
}

func (t *snapshotTable) mount(prefix string, fsys FileSystem) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var current []mount
	if p := t.mounts.Load(); p != nil {
		current = *p
	}
	next := append(slices.Clone(current), mount{prefix: prefix, fsys: fsys})
	slices.SortFunc(next, func(a, b mount) int { return len(b.prefix) - len(a.prefix) })
	t.mounts.Store(&next)
}

func (t *snapshotTable) resolve(name string) (mount, bool) {
	p := t.mounts.Load()
	if p == nil {
		return mount{}, false
	}
	return scan(*p, name)
}

// scan is the longest-prefix search the strategies share, lexical resolution
// included, so a benchmark measures the synchronisation and not four different
// searches.
func scan(mounts []mount, name string) (mount, bool) {
	name = rooted(name)
	for _, m := range mounts {
		if _, ok := m.holds(name); ok {
			return m, true
		}
	}
	return mount{}, false
}

// resolver is what the benchmark drives: a table that can be filled and read.
type resolver interface {
	mount(prefix string, fsys FileSystem)
	resolve(name string) (mount, bool)
}

// mountTable adapts the shipped table to the benchmark's shape.
type mountTable struct{ *MountTable }

func (t mountTable) mount(prefix string, fsys FileSystem) {
	if err := t.Mount(prefix, fsys); err != nil {
		panic(err)
	}
}

func (t mountTable) resolve(name string) (mount, bool) {
	m, _, err := t.MountTable.resolve(name)
	return m, err == nil
}

// benchSpread is the key space: enough mounts that the linear scan is real
// work, and enough distinct segments that partitioning has something to
// separate.
const benchSpread = 16

// fill mounts a root plus benchSpread segments, then returns the paths a reader
// asks for, spread evenly across them.
//
// The plugins are nil because resolution never calls one: the benchmark is
// about finding the mount, not about what the mount then does.
func fill(t resolver) []string {
	t.mount("/", nil)
	paths := make([]string, 0, benchSpread)
	for i := range benchSpread {
		prefix := fmt.Sprintf("/seg%02d", i)
		t.mount(prefix, nil)
		paths = append(paths, path.Join(prefix, "deep", "leaf"))
	}
	return paths
}

// benchmarkResolve drives one strategy from every core at once. Contention, if
// any, is the gap between this and the same table read by one goroutine.
func benchmarkResolve(b *testing.B, t resolver) {
	paths := fill(t)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if _, ok := t.resolve(paths[i%len(paths)]); !ok {
				b.Fatal("no mount holds a path that was mounted")
			}
			i++
		}
	})
}

func BenchmarkResolveRWMutex(b *testing.B)  { benchmarkResolve(b, &rwmutexTable{}) }
func BenchmarkResolveSharded(b *testing.B)  { benchmarkResolve(b, newShardedTable(benchSpread)) }
func BenchmarkResolveSnapshot(b *testing.B) { benchmarkResolve(b, &snapshotTable{}) }
func BenchmarkResolveShipped(b *testing.B)  { benchmarkResolve(b, mountTable{NewMountTable()}) }
