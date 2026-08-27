package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
)

// Lifecycle creates, lists and deletes workspaces across every area one
// occupies. An area is a root in the tree that holds part of a workspace — the
// files mount, the vector index, the graph, the key space, the queue — and the
// list of them is written down here once, because a workspace that is deleted
// from four areas out of five has not been deleted.
//
// Nothing here knows what an area stores. Every area is reached through the one
// FileSystem the mount table presents, so a kind that is a tree and a kind that
// is a keyspace are created and deleted by the same two verbs, and a kind added
// later joins by being mounted and named as an area.
type Lifecycle struct {
	fsys  vfs.FileSystem
	areas []string
}

// NewLifecycle returns a lifecycle over fsys covering areas, each of which must
// be a rooted, clean path such as "/files" — the mount table's rule for a
// prefix, because an area is where a mount was put.
//
// A lifecycle over no areas is refused rather than allowed to be a Delete that
// removes nothing and says it succeeded. Areas come from config, so this is the
// startup failure the invalid config is entitled to.
func NewLifecycle(fsys vfs.FileSystem, areas ...string) (*Lifecycle, error) {
	if len(areas) == 0 {
		return nil, fmt.Errorf("lifecycle over no areas: %w", fs.ErrInvalid)
	}
	for _, area := range areas {
		if !strings.HasPrefix(area, "/") || path.Clean(area) != area {
			return nil, fmt.Errorf("area %q is not a rooted clean path: %w", area, fs.ErrInvalid)
		}
	}
	return &Lifecycle{fsys: fsys, areas: slices.Clone(areas)}, nil
}

// Create makes s exist in every area.
//
// It is idempotent because Mkdir is: a workspace already there keeps what is in
// it, so creating one twice is creating it once. That is what lets a caller
// that cannot remember whether it created a workspace create it again.
//
// An area whose kind is a keyspace has no directory to make and quietly makes
// none, which is the descriptor speaking rather than a failure: a workspace
// begins to exist there when something is written under it.
func (l *Lifecycle) Create(ctx context.Context, s Scope) error {
	return l.across(ctx, "create", s, l.fsys.Mkdir)
}

// Delete removes s and everything under it from every area: the files, the
// vectors, the graph nodes, the keys and the queue entries alike, because all
// of them are paths under the same derived suffix.
//
// A workspace that is not there is not an error, so a delete that failed
// halfway is completed by running it again.
func (l *Lifecycle) Delete(ctx context.Context, s Scope) error {
	return l.across(ctx, "delete", s, l.fsys.RemoveAll)
}

// across runs verb at s's path under every area and reports what all of them
// said, not what the first one to fail said. A mutation stops leaving data
// behind only if every area is attempted: abandoning the rest because one
// refused is how a deleted workspace stays readable in the three areas the loop
// never reached.
//
// A scope names one workspace here. Deleting user scope would remove every
// workspace that user has under a verb named for one, and creating it would
// make a root with nothing in it, so both are refused.
func (l *Lifecycle) across(ctx context.Context, verb string, s Scope, act func(context.Context, string) error) error {
	if s.Workspace == "" {
		return fmt.Errorf("%s: scope names no workspace: %w", verb, fs.ErrInvalid)
	}
	paths, err := l.paths(s)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}

	errs := make([]error, 0, len(paths))
	for _, p := range paths {
		if err := act(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("%s %q: %w", verb, p, err))
		}
	}
	return errors.Join(errs...)
}

// paths is where s stands in every area. A scope that is not valid is invalid
// everywhere, so it is reported once rather than once per area.
func (l *Lifecycle) paths(s Scope) ([]string, error) {
	paths := make([]string, len(l.areas))
	for i, area := range l.areas {
		p, err := s.Path(area, "")
		if err != nil {
			return nil, err
		}
		paths[i] = p
	}
	return paths, nil
}

// List names the workspaces user has, sorted, each named once however many
// areas hold part of it. A user with nothing has no workspaces rather than an
// error: user scope is a path that comes into existence when something is put
// under it.
//
// Only a directory is a workspace. A keyspace answers for a prefix with the
// directory bit set and for a key without it, which is what separates a
// workspace from a key the user wrote in user scope beside it.
//
// A read that fails is not answered with the part that succeeded. A short list
// of workspaces is indistinguishable from a correct one, and a caller asking
// which workspaces exist is usually about to act on the ones it is not told
// about.
func (l *Lifecycle) List(ctx context.Context, user string) ([]string, error) {
	found := map[string]struct{}{}
	for _, area := range l.areas {
		p, err := Scope{User: user}.Path(area, "")
		if err != nil {
			return nil, fmt.Errorf("list: %w", err)
		}

		entries, err := l.fsys.ReadDir(ctx, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", p, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				found[entry.Name] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(found)), nil
}
