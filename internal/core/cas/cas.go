// Package cas stores content under the digest of its own bytes, so that
// identical content is one object however many callers write it.
//
// It is not a storage kind of its own. A Store is a shape over the FileSystem
// port: the same code is an object store on disk, in memory or in a bucket
// depending only on which mount it was handed, which is why nothing here needs
// a port of its own and no second implementation is nameable.
//
// Content addressing is what makes that safe. A name is derived from content
// rather than chosen, so two writers racing on one object write the same bytes
// to the same path, and a reader either sees the object or does not.
package cas

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Store is the content-addressed object store over one mount, rooted at one
// path within it.
//
// Objects are laid out as "<root>/<algorithm>/<first two hex digits>/<rest>".
// The fanout keeps a tree's directories small; over a mount that reports the
// object-store descriptor the elements are prefix and the layout costs nothing.
type Store struct {
	// sweeps excludes a sweep from what is being stored, and lives here rather
	// than on a caller because the store is the thing swept. Retain and Sweep
	// are how it is taken.
	sweeps sync.RWMutex

	fsys vfs.FileSystem
	root string
}

// New returns a store keeping its objects beneath root in fsys. The root is
// made when the first object lands, so a store over an empty mount needs no
// setup.
func New(fsys vfs.FileSystem, root string) *Store {
	return &Store{fsys: fsys, root: path.Join("/", root)}
}

// Put stores data and returns its digest. Content that is already stored is not
// written again: the digest names it, and what is there already hashes to the
// same thing.
func (s *Store) Put(ctx context.Context, data []byte) (Digest, error) {
	digest := Compute(data)
	name, err := s.path(digest)
	if err != nil {
		return Digest{}, err
	}

	stored, err := s.stored(ctx, name)
	if err != nil {
		return Digest{}, fmt.Errorf("cas put %s: %w", digest, err)
	}
	if stored {
		return digest, nil
	}

	if err := s.fsys.Mkdir(ctx, path.Dir(name)); err != nil {
		return Digest{}, fmt.Errorf("cas put %s: %w", digest, err)
	}
	if err := s.fsys.Write(ctx, name, data); err != nil {
		return Digest{}, fmt.Errorf("cas put %s: %w", digest, err)
	}
	return digest, nil
}

// Get returns the content digest names, or an error wrapping fs.ErrNotExist if
// nothing is stored under it.
//
// What comes back is hashed before it is handed over. A store whose objects can
// be reached as ordinary paths — every mount here — cannot promise that what
// was written is what is read, and a caller that asked by digest is entitled to
// content that answers to it rather than to a silently different version.
func (s *Store) Get(ctx context.Context, digest Digest) ([]byte, error) {
	name, err := s.path(digest)
	if err != nil {
		return nil, err
	}

	data, err := s.fsys.Read(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("cas get %s: %w", digest, err)
	}
	if stored := Compute(data); stored != digest {
		return nil, fmt.Errorf("cas get %s: content hashes to %s: %w", digest, stored, fs.ErrInvalid)
	}
	return data, nil
}

// Has reports whether digest names something stored, without reading it.
func (s *Store) Has(ctx context.Context, digest Digest) (bool, error) {
	name, err := s.path(digest)
	if err != nil {
		return false, err
	}

	stored, err := s.stored(ctx, name)
	if err != nil {
		return false, fmt.Errorf("cas has %s: %w", digest, err)
	}
	return stored, nil
}

// Remove deletes the object digest names. Removing what is not there is not an
// error, because the caller asked for content to be gone and it is.
//
// Nothing here knows whether anything still refers to the object. Deciding that
// is the versioner's, and this is the verb it decides with.
func (s *Store) Remove(ctx context.Context, digest Digest) error {
	name, err := s.path(digest)
	if err != nil {
		return err
	}

	if err := s.fsys.Remove(ctx, name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cas remove %s: %w", digest, err)
	}
	return nil
}

// stored reports whether name exists in the mount, which for a name derived
// from content is the whole of what there is to ask about it.
func (s *Store) stored(ctx context.Context, name string) (bool, error) {
	switch _, err := s.fsys.Stat(ctx, name); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// path is where digest's content lives in the mount. Only the zero digest has
// no path: it names nothing, and it reaches here from a caller that read an
// error from ParseDigest and carried on.
func (s *Store) path(digest Digest) (string, error) {
	if digest.IsZero() {
		return "", fmt.Errorf("cas: the zero digest names nothing: %w", fs.ErrInvalid)
	}
	return path.Join(s.root, digest.algorithm, digest.hex[:2], digest.hex[2:]), nil
}
