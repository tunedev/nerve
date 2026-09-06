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
	return l.across(ctx, "create", s, func(fsys vfs.FileSystem, name string) error {
		return fsys.Mkdir(ctx, name)
	})
}

// Delete removes s and everything under it from every area: the files, the
// vectors, the graph nodes, the keys and the queue entries alike, because all
// of them are paths under the same derived suffix.
//
// A workspace that is not there is not an error, so a delete that failed
// halfway is completed by running it again.
func (l *Lifecycle) Delete(ctx context.Context, s Scope) error {
	return l.across(ctx, "delete", s, func(fsys vfs.FileSystem, name string) error {
		return fsys.RemoveAll(ctx, name)
	})
}

// across runs verb on s's workspace in every area and reports what all of them
// said, not what the first one to fail said. A mutation stops leaving data
// behind only if every area is attempted: abandoning the rest because one
// refused is how a deleted workspace stays readable in the three areas the loop
// never reached.
//
// The workspace is named inside the user scope rather than reached by a path
// derived down to it. The string is the same either way; who resolves it is
// not, and that is the whole difference — a path handed to the tree is resolved
// by the tree, which follows a link standing at any component of it.
//
// A scope names one workspace here. Deleting user scope would remove every
// workspace that user has under a verb named for one, and creating it would
// make a root with nothing in it, so both are refused.
func (l *Lifecycle) across(ctx context.Context, verb string, s Scope, act func(vfs.FileSystem, string) error) error {
	if s.Workspace == "" {
		return fmt.Errorf("%s: scope names no workspace: %w", verb, fs.ErrInvalid)
	}
	held, err := l.holding(s)
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}

	errs := make([]error, 0, len(held))
	for _, area := range held {
		if err := act(area.held, s.Workspace); err != nil {
			errs = append(errs, fmt.Errorf("%s %q: %w", verb, area.at, err))
		}
	}
	return errors.Join(errs...)
}

// scoped is one area with the user scope inside it already narrowed to. held is
// what a verb acts through; at is where the scope stands, carried for what an
// error says rather than for anything to resolve.
type scoped struct {
	at   string
	held vfs.FileSystem
}

// holding narrows every area to the subtree holding every workspace s's user
// has. The workspaces are names in it, so a verb names one rather than spelling
// a path down to it.
//
// It is View that makes this a boundary rather than a prefix. An area that
// hands out a subtree hands out the one a walk refusing a link at each
// component reached, so a link planted anywhere on the scope path is refused
// instead of followed; an area that hands out none is contained by the
// derivation alone, which is the whole containment a kind that resolves nothing
// but the name it was given needs.
//
// A scope that is not valid is invalid in every area, so it is reported once
// rather than once per area.
func (l *Lifecycle) holding(s Scope) ([]scoped, error) {
	user := Scope{User: s.User}
	held := make([]scoped, len(l.areas))
	for i, area := range l.areas {
		at, err := s.Path(area, "")
		if err != nil {
			return nil, err
		}
		fsys, err := user.View(l.fsys, area)
		if err != nil {
			return nil, err
		}
		held[i] = scoped{at: at, held: fsys}
	}
	return held, nil
}

// List names the workspaces user has, sorted, each named once however many
// areas hold part of it. A user with nothing has no workspaces rather than an
// error: user scope is a path that comes into existence when something is put
// under it.
//
// Only a directory is a workspace. A keyspace answers for a prefix with the
// directory bit set and for a key without it, which is what separates a
// workspace from a key the user wrote in user scope beside it. A link is not a
// directory either, so an entry standing for another user's workspace names
// none of his.
//
// A read that fails is not answered with the part that succeeded. A short list
// of workspaces is indistinguishable from a correct one, and a caller asking
// which workspaces exist is usually about to act on the ones it is not told
// about.
func (l *Lifecycle) List(ctx context.Context, user string) ([]string, error) {
	held, err := l.holding(Scope{User: user})
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	found := map[string]struct{}{}
	for _, area := range held {
		entries, err := area.held.ReadDir(ctx, "/")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", area.at, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				found[entry.Name] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(found)), nil
}
