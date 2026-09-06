package vfstest_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/vfstest"
)

// The suite is only worth running if a claimed descriptor changes what it
// asserts. Each mount below claims one and honours it, so the branch the
// descriptor selects is exercised rather than described. Three wrap the tree in
// memory that claims nothing; the object store is a flat keyspace, because a
// tree cannot hold a key beside a prefix of the same name and being asked what
// that name is the one thing an object store faces and a tree never does.

// readOnlyTree refuses every mutating verb, including the ones an accessor
// reaches: a refusal one call away from being bypassed is not a refusal.
type readOnlyTree struct{ *memfs.FS }

func (readOnlyTree) Capabilities(string) vfs.Caps                 { return vfs.Caps{ReadOnly: true} }
func (readOnlyTree) Create(context.Context, string) error         { return fs.ErrPermission }
func (readOnlyTree) Mkdir(context.Context, string) error          { return fs.ErrPermission }
func (readOnlyTree) Remove(context.Context, string) error         { return fs.ErrPermission }
func (readOnlyTree) RemoveAll(context.Context, string) error      { return fs.ErrPermission }
func (readOnlyTree) Write(context.Context, string, []byte) error  { return fs.ErrPermission }
func (readOnlyTree) Rename(context.Context, string, string) error { return fs.ErrPermission }
func (readOnlyTree) Chmod(context.Context, string, fs.FileMode) error {
	return fs.ErrPermission
}
func (r readOnlyTree) AsStreamer() vfs.Streamer   { return r }
func (r readOnlyTree) AsTruncater() vfs.Truncater { return r }
func (r readOnlyTree) AsSubber() vfs.Subber       { return nil }

func (r readOnlyTree) OpenRead(ctx context.Context, name string) (io.ReadCloser, error) {
	return r.FS.AsStreamer().OpenRead(ctx, name)
}
func (readOnlyTree) OpenWrite(context.Context, string) (io.WriteCloser, error) {
	return nil, fs.ErrPermission
}
func (readOnlyTree) Truncate(context.Context, string, int64) error { return fs.ErrPermission }

// appendOnlyLog never replaces or shortens what is already there. It answers no
// optional verb set: a truncate verb would undo the claim, and a streamed write
// over a byte slice would replace rather than add.
type appendOnlyLog struct{ *memfs.FS }

func (appendOnlyLog) Capabilities(string) vfs.Caps { return vfs.Caps{AppendOnly: true} }
func (appendOnlyLog) AsStreamer() vfs.Streamer     { return nil }
func (appendOnlyLog) AsSubber() vfs.Subber         { return nil }
func (appendOnlyLog) AsTruncater() vfs.Truncater   { return nil }

func (a appendOnlyLog) Write(ctx context.Context, name string, data []byte) error {
	existing, err := a.Read(ctx, name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return a.FS.Write(ctx, name, append(existing, data...))
}

// consumingQueue hands an entry to one reader and to no one after it.
type consumingQueue struct{ *memfs.FS }

func (consumingQueue) Capabilities(string) vfs.Caps { return vfs.Caps{ReadDestructive: true} }
func (consumingQueue) AsStreamer() vfs.Streamer     { return nil }
func (consumingQueue) AsSubber() vfs.Subber         { return nil }
func (consumingQueue) AsTruncater() vfs.Truncater   { return nil }

func (q consumingQueue) Read(ctx context.Context, name string) ([]byte, error) {
	content, err := q.FS.Read(ctx, name)
	if err != nil {
		return nil, err
	}
	return content, q.Remove(ctx, name)
}

func TestReadOnlyMountRunsTheSuite(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return readOnlyTree{memfs.New()} },
	})
}

func TestAppendOnlyMountRunsTheSuite(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return appendOnlyLog{memfs.New()} },
	})
}

func TestReadDestructiveMountRunsTheSuite(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return consumingQueue{memfs.New()} },
	})
}

func TestObjectStoreMountRunsTheSuite(t *testing.T) {
	vfstest.Run(t, vfstest.Plugin{
		New: func(*testing.T) vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "objects") },
	})
}
