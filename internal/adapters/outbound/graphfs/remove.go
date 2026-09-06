package graphfs

import (
	"context"
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
	case atRoot, atCorpus, atNeighbours:
		return fmt.Errorf("remove %q: the mount itself: %w", name, fs.ErrInvalid)
	default:
		return f.refuse("remove", name, spot)
	}
}

// RemoveAll takes name and everything under it out of the graph. A name that is
// not there is not an error, and the texts directory is emptied rather than
// removed, which is how a graph is cleared.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	spot, key := locate(name)
	switch spot {
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
