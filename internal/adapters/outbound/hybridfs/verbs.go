package hybridfs

import (
	"context"
	"fmt"
	"io/fs"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Read hands back what is at name: the description of the fusion, or the answer
// to a question, which is the whole point of the mount — several rankings
// reached and folded through the same verb that reads a file.
func (f *FS) Read(ctx context.Context, name string) ([]byte, error) {
	spot, question := locate(name)
	switch spot {
	case atConfig:
		return f.description(), nil
	case atQuestion:
		answer, err := f.fuse(ctx, question)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", name, err)
		}
		return answer, nil
	case atRoot, atQuery:
		return nil, fmt.Errorf("read %q: a directory: %w", name, fs.ErrInvalid)
	default:
		return nil, fmt.Errorf("read %q: %w", name, fs.ErrNotExist)
	}
}

// Stat describes name.
//
// A question has no stored existence, so describing one costs what answering it
// costs: the size reported is the size of the answer, and getting it means
// asking every ranker. A caller that wants the answer should read rather than
// stat first.
func (f *FS) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	spot, question := locate(name)
	switch spot {
	case atRoot:
		return dirInfo(separator), nil
	case atQuery:
		return dirInfo(Query), nil
	case atConfig:
		return vfs.FileInfo{Name: Config, Size: int64(len(f.description())), Mode: fileMode}, nil
	case atQuestion:
		answer, err := f.fuse(ctx, question)
		if err != nil {
			return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, err)
		}
		return vfs.FileInfo{Name: path.Base(question), Size: int64(len(answer)), Mode: fileMode}, nil
	default:
		return vfs.FileInfo{}, fmt.Errorf("stat %q: %w", name, fs.ErrNotExist)
	}
}

// ReadDir lists what sits directly under name.
//
// The questions list nothing, ever. Every name under them answers, so there is
// nothing to enumerate: a listing would either be empty or be the set of
// questions somebody happened to ask, and only the first of those is true.
func (f *FS) ReadDir(_ context.Context, name string) ([]vfs.FileInfo, error) {
	switch spot, _ := locate(name); spot {
	case atRoot:
		return []vfs.FileInfo{
			{Name: Config, Size: int64(len(f.description())), Mode: fileMode},
			dirInfo(Query),
		}, nil
	case atQuery:
		return nil, nil
	default:
		return nil, fmt.Errorf("read_dir %q: %w", name, fs.ErrNotExist)
	}
}

// Create refuses. A fusion holds nothing of its own.
func (f *FS) Create(_ context.Context, name string) error { return refuse("create", name) }

// Mkdir refuses. The mount's own two paths are its shape, not entries a caller
// makes.
func (f *FS) Mkdir(_ context.Context, name string) error { return refuse("mkdir", name) }

// Remove refuses.
func (f *FS) Remove(_ context.Context, name string) error { return refuse("remove", name) }

// RemoveAll refuses. It is a refusal rather than a silent success even at a
// path that holds nothing: a caller clearing a fusion has misunderstood where
// the keys live, and the mounts behind it are where they are removed.
func (f *FS) RemoveAll(_ context.Context, name string) error { return refuse("remove_all", name) }

// Write refuses. A key an answer names belongs to a source, and the place to
// change one is the mount that holds it.
func (f *FS) Write(_ context.Context, name string, _ []byte) error { return refuse("write", name) }

// Rename refuses.
func (f *FS) Rename(_ context.Context, oldPath, _ string) error { return refuse("rename", oldPath) }

// Chmod refuses. Nothing here has a mode of its own to set.
func (f *FS) Chmod(_ context.Context, name string, _ fs.FileMode) error {
	return refuse("chmod", name)
}
