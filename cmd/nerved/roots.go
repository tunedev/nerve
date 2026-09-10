package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/tunedev/nerve/internal/core/kv"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// resourceStore builds the store the resources of every workspace stand in,
// under root on the tree the mounts present. A build that names no root holds
// no store rather than one over nowhere, which is the laptop with nothing to
// upload to.
//
// The key is read here rather than resolved with the rest of the config,
// because it is the one value this process needs that the config document must
// never carry.
func resourceStore(fsys vfs.FileSystem, root, keyFile string) (*resource.Store, error) {
	if root == "" {
		return nil, nil
	}

	key, err := signingKey(keyFile)
	if err != nil {
		return nil, err
	}
	signer, err := resource.NewSigner(key, time.Now)
	if err != nil {
		return nil, err
	}
	return resource.NewStore(fsys, root, signer)
}

// scratchSpace builds the space the scratch state of every workspace stands in.
// It is the same decision resourceStore makes about a root, made the same way,
// and it needs no secret: nothing hands a scratch key out to anybody.
func scratchSpace(fsys vfs.FileSystem, root string) (*kv.Space, error) {
	if root == "" {
		return nil, nil
	}
	return kv.NewSpace(fsys, root)
}

// signingKey reads the key download grants are signed with out of the file at
// path.
//
// A file rather than a setting, because the key outlives and rotates apart from
// everything beside it: a mounted secret in a cluster, a file the operator
// wrote on the laptop, replaced without the config document changing. Config
// names where it is; no layer of the config can hold it, so no layer can log it
// or echo it back.
//
// Trailing line endings go, because a key written with a shell redirect carries
// a newline nobody meant to sign with. A file with nothing else in it is
// refused: an empty key is what NewSigner exists to reject, and an operator who
// created the file and never filled it should hear so at boot.
func signingKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("download-key-file: %w", err)
	}

	key = bytes.TrimRight(key, "\r\n")
	if len(key) == 0 {
		return nil, fmt.Errorf("download-key-file %s holds no key: %w", path, fs.ErrInvalid)
	}
	return key, nil
}
