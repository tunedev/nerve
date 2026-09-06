package graphfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// Remove takes the text at name out of the graph, and everything it said with
// it. A name that is only a prefix is not there to remove: no directory exists
// apart from the texts under it, and removing those is what RemoveAll is for.
//
// What an edge stood on shrinks by one. A claim two documents made survives the
// removal of either with less support behind it, which is the honest answer:
// the graph still holds a text that says so.
func (f *FS) Remove(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
	case atDoc:
		return f.commit(ctx, func(documents map[string]document) error {
			if _, there := documents[key]; !there {
				return fmt.Errorf("remove %q: %w", name, fs.ErrNotExist)
			}
			delete(documents, key)
			return nil
		})
	case atSnapshot:
		if err := f.drop(ctx, key); err != nil {
			return fmt.Errorf("remove %q: %w", name, err)
		}
		return nil
	case atRoot, atCorpus, atNeighbours, atSnapshots:
		return fmt.Errorf("remove %q: the mount itself: %w", name, fs.ErrInvalid)
	default:
		return f.refuse("remove", name, spot)
	}
}

// RemoveAll takes name and everything under it out of the graph. A name that is
// not there is not an error, and the texts directory is emptied rather than
// removed, which is how a graph is cleared.
//
// Under the snapshots it forgets one label or all of them. The graph is not
// touched either way: a snapshot is what the graph was, and forgetting it says
// nothing about what the graph is.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
	case atSnapshot:
		return f.dropSnapshots(ctx, name, key)
	case atSnapshots:
		return f.dropSnapshots(ctx, name)
	case atCorpus, atDoc:
		return f.commit(ctx, func(documents map[string]document) error {
			delete(documents, key)
			for held := range documents {
				if strings.HasPrefix(held, prefixOf(key)) {
					delete(documents, held)
				}
			}
			return nil
		})
	default:
		return f.refuse("remove_all", name, spot)
	}
}

// dropSnapshots forgets the labels named, or every label when none is. A label
// already gone is not an error, which is what makes removing a subtree
// idempotent here as everywhere else.
func (f *FS) dropSnapshots(ctx context.Context, name string, labels ...string) error {
	if len(labels) == 0 {
		kept, err := f.snapshots.Labels(ctx, f.name)
		if err != nil {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
		labels = kept
	}

	for _, label := range labels {
		if err := f.drop(ctx, label); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove_all %q: %w", name, err)
		}
	}
	return nil
}

// drop forgets one snapshot, refusing a name that is not a label the way every
// other verb reaching the snapshots does.
func (f *FS) drop(ctx context.Context, label string) error {
	if err := checkLabel(label); err != nil {
		return err
	}
	return f.snapshots.Drop(ctx, f.name, label)
}
