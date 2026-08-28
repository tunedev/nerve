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
	at, err := sp.at(s, namespace, key)
	if err != nil {
		return nil, fmt.Errorf("get %q/%q: %w", namespace, key, err)
	}
	value, err := sp.fsys.Read(ctx, at)
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
	at, err := sp.at(s, namespace, key)
	if err != nil {
		return fmt.Errorf("put %q/%q: %w", namespace, key, err)
	}
	if err := sp.fsys.Mkdir(ctx, path.Dir(at)); err != nil {
		return fmt.Errorf("put %q/%q: %w", namespace, key, err)
	}
	if err := sp.fsys.Write(ctx, at, value); err != nil {
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
	at, err := sp.at(s, namespace, key)
	if err != nil {
		return fmt.Errorf("delete %q/%q: %w", namespace, key, err)
	}
	if err := sp.fsys.Remove(ctx, at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %q/%q: %w", namespace, key, err)
	}
	return nil
}

// at is where key stands in s under namespace.
//
// User scope is refused: a scope with no workspace derives the path holding
// every workspace that user has, so a namespace written there would stand where
// a workspace belongs and be listed as one.
func (sp *Space) at(s workspace.Scope, namespace, key string) (string, error) {
	if err := validSegment("namespace", namespace); err != nil {
		return "", err
	}
	if err := validSegment("key", key); err != nil {
		return "", err
	}
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(sp.root, path.Join(namespace, key))
}
