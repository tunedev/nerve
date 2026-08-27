package cas

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// fanoutLength is how many digits of a digest name the directory an object sits
// in. It is the layout's, so only a name of exactly that length can be one.
const fanoutLength = 2

// List returns every digest the store holds.
//
// A sweep is what needs it: deciding an object is referred to by nothing needs
// the set of objects, and only this package knows where they are. Nothing is
// read but the listings, so the cost is the tree's shape rather than its
// content.
//
// Anything under the root that is not laid out as an object is skipped rather
// than reported. The store shares its mount with whatever else was handed the
// same root, and a sweep that failed on a stray file would be a sweep that
// never ran.
func (s *Store) List(ctx context.Context) ([]Digest, error) {
	var digests []Digest

	algorithms, err := s.children(ctx, s.root)
	if err != nil {
		return nil, err
	}
	for _, algorithm := range algorithms {
		if !algorithm.IsDir() {
			continue
		}
		fanouts, err := s.children(ctx, path.Join(s.root, algorithm.Name))
		if err != nil {
			return nil, err
		}
		for _, fanout := range fanouts {
			if !fanout.IsDir() || len(fanout.Name) != fanoutLength {
				continue
			}
			objects, err := s.children(ctx, path.Join(s.root, algorithm.Name, fanout.Name))
			if err != nil {
				return nil, err
			}
			for _, object := range objects {
				digest, err := ParseDigest(algorithm.Name + ":" + fanout.Name + object.Name)
				if object.IsDir() || err != nil {
					continue
				}
				digests = append(digests, digest)
			}
		}
	}
	return digests, nil
}

// children lists what sits directly under name, and nothing when name is not
// there: a store that has never been written to holds no objects rather than
// failing to say so.
func (s *Store) children(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	entries, err := s.fsys.ReadDir(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cas list %q: %w", name, err)
	}
	return entries, nil
}
