package kv

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Space is the scratch state of every workspace on the tree under root,
// reached by the namespace and key the caller names inside its own scope.
type Space struct {
	fsys vfs.FileSystem
	root string
}

// NewSpace returns the space keeping scratch state under root on fsys.
//
// root must be a rooted, clean path such as "/scratch" — the mount table's rule
// for a prefix, because a space is where a mount was put.
func NewSpace(fsys vfs.FileSystem, root string) (*Space, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("scratch root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	return &Space{fsys: fsys, root: root}, nil
}

// Get returns the whole value key holds in s under namespace. A key nothing was
// put under reports fs.ErrNotExist, which is what "there is no such state" is.
func (sp *Space) Get(ctx context.Context, s workspace.Scope, namespace, key string) ([]byte, error) {
	held, at, err := sp.held(s, namespace, key)
	if err != nil {
		return nil, fmt.Errorf("get %q/%q: %w", namespace, key, err)
	}
	value, err := held.Read(ctx, at)
	if err != nil {
		return nil, fmt.Errorf("get %q/%q: %w", namespace, key, err)
	}
	return value, nil
}

// Put stores value under key in s under namespace, replacing whatever was
// there.
//
// Replacing, because the verb set has no update: the way to change what a key
// holds is to put it again. The namespace is made before the write, which is
// what a tree needs and what a keyspace has nothing to do about.
func (sp *Space) Put(ctx context.Context, s workspace.Scope, namespace, key string, value []byte) error {
	held, at, err := sp.held(s, namespace, key)
	if err != nil {
		return fmt.Errorf("put %q/%q: %w", namespace, key, err)
	}
	if err := held.Mkdir(ctx, path.Dir(at)); err != nil {
		return fmt.Errorf("put %q/%q: %w", namespace, key, err)
	}
	if err := held.Write(ctx, at, value); err != nil {
		return fmt.Errorf("put %q/%q: %w", namespace, key, err)
	}
	return nil
}

// Delete removes what key holds in s under namespace. A key that is not there
// is not an error, so a delete that failed halfway is completed by running it
// again.
//
// It removes the key and nothing under it. A keyspace lets a key and a prefix
// share a name, so reaching beneath the key would take out state something else
// on the same mount put there under a longer name.
func (sp *Space) Delete(ctx context.Context, s workspace.Scope, namespace, key string) error {
	held, at, err := sp.held(s, namespace, key)
	if err != nil {
		return fmt.Errorf("delete %q/%q: %w", namespace, key, err)
	}
	if err := held.Remove(ctx, at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %q/%q: %w", namespace, key, err)
	}
	return nil
}

// held is the space as a caller inside s sees it, and where key stands in it
// under namespace.
//
// The view is what makes the scope a boundary rather than a prefix: the name is
// resolved by the subtree the tree beneath hands out, so a link standing at any
// component of the scope path is refused instead of followed. A path derived
// down to the key and handed to the whole area is resolved by the whole area,
// which follows that link.
//
// User scope is refused: a scope with no workspace derives the path holding
// every workspace that user has, so a namespace written there would stand where
// a workspace belongs and be listed as one.
func (sp *Space) held(s workspace.Scope, namespace, key string) (vfs.FileSystem, string, error) {
	if err := vfs.ValidSegment("namespace", namespace); err != nil {
		return nil, "", err
	}
	if err := vfs.ValidSegment("key", key); err != nil {
		return nil, "", err
	}
	if s.Workspace == "" {
		return nil, "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	held, err := s.View(sp.fsys, sp.root)
	if err != nil {
		return nil, "", err
	}
	return held, path.Join("/", namespace, key), nil
}
