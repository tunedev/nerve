package resource

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Content returns the bytes of the resource named name in s, as a stream the
// caller closes.
//
// It is the read a stage makes of the resource it was handed. A grant is what
// something outside the process needs to reach one file and nothing else;
// inside, the caller already holds the scope, and minting a token to hand
// straight back to the same store would be a signature checking itself.
//
// A resource that has not been finalised has no content to read: what stands
// there is whatever the upload has managed so far, and a reader would take that
// for the whole file.
func (st *Store) Content(ctx context.Context, s workspace.Scope, name string) (io.ReadCloser, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return nil, fmt.Errorf("read resource: %w", err)
	}
	held, err := st.read(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("read resource %q: %w", name, err)
	}
	if !held.Complete {
		return nil, fmt.Errorf("read resource %q: it has not been completed: %w", name, fs.ErrNotExist)
	}

	stream := st.fsys.AsStreamer()
	if stream == nil {
		return nil, fmt.Errorf("read resource %q: the mount has no streaming verbs: %w", name, fs.ErrInvalid)
	}
	content, err := stream.OpenRead(ctx, path.Join(dir, contentFile))
	if err != nil {
		return nil, fmt.Errorf("read resource %q: %w", name, err)
	}
	return content, nil
}

// Derived returns the sidecar named kind standing beside the resource named
// name in s. A sidecar nothing has written reports fs.ErrNotExist, which is how
// a caller tells a resource whose ingestion has run from one whose has not.
func (st *Store) Derived(ctx context.Context, s workspace.Scope, name, kind string) ([]byte, error) {
	at, err := st.sidecar(s, name, kind)
	if err != nil {
		return nil, fmt.Errorf("read %q of resource: %w", kind, err)
	}
	data, err := st.fsys.Read(ctx, at)
	if err != nil {
		return nil, fmt.Errorf("read %q of resource %q: %w", kind, name, err)
	}
	return data, nil
}

// SetDerived records data as the sidecar named kind beside the resource named
// name in s, replacing whatever an earlier run left there.
//
// A resource nobody declared has nothing to stand beside, and is refused rather
// than given a directory of its own: a sidecar arriving before its content
// would be a stage acting on a name it invented.
//
// The sidecar is not a resource, so writing one queues no second ingestion of
// the same file — only a resource's meta file declares one.
func (st *Store) SetDerived(ctx context.Context, s workspace.Scope, name, kind string, data []byte) error {
	at, err := st.sidecar(s, name, kind)
	if err != nil {
		return fmt.Errorf("write %q of resource: %w", kind, err)
	}
	if _, err := st.read(ctx, path.Dir(at)); err != nil {
		return fmt.Errorf("write %q of resource %q: %w", kind, name, err)
	}
	if err := st.fsys.Write(ctx, at, data); err != nil {
		return fmt.Errorf("write %q of resource %q: %w", kind, name, err)
	}
	return nil
}

// sidecar is where the derivation named kind stands beside the resource named
// name in s.
//
// A kind is a flat segment for the reason a resource name is: one carrying a
// separator names a place inside another file. The two files a resource is
// made of are refused on top of that, because a sidecar named after either
// would write over the content or over what the resource says about itself.
func (st *Store) sidecar(s workspace.Scope, name, kind string) (string, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return "", err
	}
	if err := vfs.ValidSegment("sidecar name", kind); err != nil {
		return "", err
	}
	if kind == metaFile || kind == contentFile {
		return "", fmt.Errorf("sidecar %q is a file the resource is made of: %w", kind, fs.ErrInvalid)
	}
	return path.Join(dir, kind), nil
}

// Derivations lists the sidecars standing beside the resource named name in s,
// by kind and sorted by name, leaving out the two files the resource itself is
// made of.
//
// It is one listing rather than a read of each kind a caller might want: a
// caller deciding what to read should not pay for every answer to find out
// which ones exist, and a mount that may be remote should not be asked once per
// kind.
//
// A resource that is not there reports fs.ErrNotExist. One whose ingestion has
// not run has no sidecars, which is an empty list rather than a refusal.
func (st *Store) Derivations(ctx context.Context, s workspace.Scope, name string) ([]string, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return nil, fmt.Errorf("list derivations of resource: %w", err)
	}
	entries, err := st.fsys.ReadDir(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("list derivations of resource %q: %w", name, err)
	}

	kinds := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name != metaFile && entry.Name != contentFile {
			kinds = append(kinds, entry.Name)
		}
	}
	return kinds, nil
}
