package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// The two files a resource is made of: what it says about itself, and the bytes
// themselves. They are separate because only one of them moves in pieces.
const (
	metaFile    = "meta.json"
	contentFile = "content"
)

// Store is the resources of every workspace on the tree under root, signed for
// download by signer.
type Store struct {
	fsys   vfs.FileSystem
	root   string
	signer *Signer
}

// NewStore returns the store keeping resources under root on fsys.
//
// root must be a rooted, clean path such as "/resources" — the mount table's
// rule for a prefix, because a store is where a mount was put. A store with no
// signer is refused rather than allowed to become a SignedDownload that panics
// the first time something asks for a link.
func NewStore(fsys vfs.FileSystem, root string, signer *Signer) (*Store, error) {
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return nil, fmt.Errorf("resource root %q is not a rooted clean path: %w", root, fs.ErrInvalid)
	}
	if signer == nil {
		return nil, fmt.Errorf("resource store with no signer: %w", fs.ErrInvalid)
	}
	return &Store{fsys: fsys, root: root, signer: signer}, nil
}

// Create declares the resource named name in s and reserves the name. No
// content arrives here: what comes back is a resource with nothing in it yet.
//
// A name already taken is refused with fs.ErrExist rather than replaced,
// because replacing would put this upload's bytes under a declaration another
// caller is still uploading against.
func (st *Store) Create(ctx context.Context, s workspace.Scope, name, contentType string) (Resource, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return Resource{}, fmt.Errorf("create resource: %w", err)
	}

	switch _, err := st.read(ctx, dir); {
	case err == nil:
		return Resource{}, fmt.Errorf("create resource %q: %w", name, fs.ErrExist)
	case !errors.Is(err, fs.ErrNotExist):
		return Resource{}, fmt.Errorf("create resource %q: %w", name, err)
	}

	if err := st.fsys.Mkdir(ctx, dir); err != nil {
		return Resource{}, fmt.Errorf("create resource %q: %w", name, err)
	}
	held := meta{ContentType: contentType}
	if err := st.write(ctx, dir, held); err != nil {
		return Resource{}, fmt.Errorf("create resource %q: %w", name, err)
	}
	return resourceOf(name, held), nil
}

// Upload streams content into the resource named name in s, replacing whatever
// an interrupted upload left there, and returns how many bytes arrived.
//
// It goes through the streaming verbs and never through Write, which is the
// whole reason the three steps are separate: the content is the part that does
// not fit in a call. A mount with no streaming verbs is refused rather than
// buffered around.
//
// A resource that has been finalised is refused with fs.ErrExist. Its digest
// names the content it was completed with, and a second upload would leave the
// resource saying one thing and holding another.
func (st *Store) Upload(ctx context.Context, s workspace.Scope, name string, content io.Reader) (int64, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return 0, fmt.Errorf("upload resource: %w", err)
	}
	held, err := st.read(ctx, dir)
	if err != nil {
		return 0, fmt.Errorf("upload resource %q: %w", name, err)
	}
	if held.Complete {
		return 0, fmt.Errorf("upload resource %q: it was completed as %s: %w", name, held.Digest, fs.ErrExist)
	}

	stream := st.fsys.AsStreamer()
	if stream == nil {
		return 0, fmt.Errorf("upload resource %q: the mount has no streaming verbs: %w", name, fs.ErrInvalid)
	}
	writer, err := stream.OpenWrite(ctx, path.Join(dir, contentFile))
	if err != nil {
		return 0, fmt.Errorf("upload resource %q: %w", name, err)
	}

	written, err := io.Copy(writer, content)
	if err != nil {
		// The close is what a half-written stream needs to let go of; the copy
		// is what went wrong, and it is what the caller is told.
		_ = writer.Close()
		return 0, fmt.Errorf("upload resource %q: %w", name, err)
	}
	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("upload resource %q: %w", name, err)
	}
	return written, nil
}

// Complete finalises the resource named name in s: it reads the uploaded
// content back through the hash, records its size and digest, and marks the
// resource downloadable.
//
// The content is hashed by streaming, so finalising a file larger than memory
// costs no more memory than uploading it did.
//
// A resource already finalised is returned as it stands. This is the call a
// client repeats when it did not hear the answer, and doing the work twice
// would compute the same digest for the same bytes.
func (st *Store) Complete(ctx context.Context, s workspace.Scope, name string) (Resource, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return Resource{}, fmt.Errorf("complete resource: %w", err)
	}
	held, err := st.read(ctx, dir)
	if err != nil {
		return Resource{}, fmt.Errorf("complete resource %q: %w", name, err)
	}
	if held.Complete {
		return resourceOf(name, held), nil
	}

	digest, size, err := st.digest(ctx, path.Join(dir, contentFile))
	if err != nil {
		return Resource{}, fmt.Errorf("complete resource %q: %w", name, err)
	}

	held.Size = size
	held.Digest = digest.String()
	held.Complete = true
	if err := st.write(ctx, dir, held); err != nil {
		return Resource{}, fmt.Errorf("complete resource %q: %w", name, err)
	}
	return resourceOf(name, held), nil
}

// Stat returns what the store knows about the resource named name in s,
// without moving its content.
//
// It is the answer Complete gave, read back by whoever arrives after it: a
// resource that has not been finalised reports itself incomplete rather than
// being refused, because "it is not ready yet" is what the caller asked.
func (st *Store) Stat(ctx context.Context, s workspace.Scope, name string) (Resource, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return Resource{}, fmt.Errorf("stat resource: %w", err)
	}
	held, err := st.read(ctx, dir)
	if err != nil {
		return Resource{}, fmt.Errorf("stat resource %q: %w", name, err)
	}
	return resourceOf(name, held), nil
}

// SignedDownload returns the grant letting its holder read the resource named
// name in s for ttl, presenting nothing else.
//
// A resource that is not finalised has no download: what stands on the mount is
// whatever the upload managed so far, and a holder of a grant would read that
// as the whole file.
func (st *Store) SignedDownload(ctx context.Context, s workspace.Scope, name string, ttl time.Duration) (Grant, error) {
	dir, err := st.dir(s, name)
	if err != nil {
		return Grant{}, fmt.Errorf("sign download: %w", err)
	}
	held, err := st.read(ctx, dir)
	if err != nil {
		return Grant{}, fmt.Errorf("sign download of %q: %w", name, err)
	}
	if !held.Complete {
		return Grant{}, fmt.Errorf("sign download of %q: it has not been completed: %w", name, fs.ErrNotExist)
	}
	return st.signer.Sign(s, name, ttl)
}

// Download returns the content token grants, as a stream the caller closes.
//
// The token is the whole of what it takes: it names the scope, so nothing
// checks a scope here that the signature has not already covered. Content the
// grant outlived reports fs.ErrNotExist, which is what a deleted resource is.
func (st *Store) Download(ctx context.Context, token string) (io.ReadCloser, error) {
	granted, err := st.signer.Verify(token)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	dir, err := st.dir(granted.Scope, granted.Name)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	stream := st.fsys.AsStreamer()
	if stream == nil {
		return nil, fmt.Errorf("download %q: the mount has no streaming verbs: %w", granted.Name, fs.ErrInvalid)
	}
	content, err := stream.OpenRead(ctx, path.Join(dir, contentFile))
	if err != nil {
		return nil, fmt.Errorf("download %q: %w", granted.Name, err)
	}
	return content, nil
}

// Delete removes the resource named name in s, its content and what it said
// about itself.
//
// A resource that is not there is not an error, so a delete that failed halfway
// is completed by running it again. Grants already signed for it are not
// tracked and are not revoked: what they name is gone, and redeeming one
// reports that.
func (st *Store) Delete(ctx context.Context, s workspace.Scope, name string) error {
	dir, err := st.dir(s, name)
	if err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	if err := st.fsys.RemoveAll(ctx, dir); err != nil {
		return fmt.Errorf("delete resource %q: %w", name, err)
	}
	return nil
}

// digest is the content at path, hashed and measured by passing through rather
// than by being held.
//
// The length comes from the same pass as the hash, so the two always describe
// the same bytes. Asking the mount how big the content is would be a second
// answer, taken at a second moment.
func (st *Store) digest(ctx context.Context, at string) (cas.Digest, int64, error) {
	stream := st.fsys.AsStreamer()
	if stream == nil {
		return cas.Digest{}, 0, fmt.Errorf("hash %q: the mount has no streaming verbs: %w", at, fs.ErrInvalid)
	}
	content, err := stream.OpenRead(ctx, at)
	if err != nil {
		return cas.Digest{}, 0, err
	}
	defer func() { _ = content.Close() }()

	measured := &counting{from: content}
	digest, err := cas.ComputeFrom(measured)
	if err != nil {
		return cas.Digest{}, 0, err
	}
	return digest, measured.read, nil
}

// counting is a reader that remembers how much passed through it.
type counting struct {
	from io.Reader
	read int64
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.from.Read(p)
	c.read += int64(n)
	return n, err
}

// read returns what the resource in dir says about itself. A directory with no
// meta file reports fs.ErrNotExist, which is what "no resource was declared
// here" is.
func (st *Store) read(ctx context.Context, dir string) (meta, error) {
	data, err := st.fsys.Read(ctx, path.Join(dir, metaFile))
	if err != nil {
		return meta{}, err
	}
	var held meta
	if err := json.Unmarshal(data, &held); err != nil {
		return meta{}, fmt.Errorf("%q: %w", path.Join(dir, metaFile), err)
	}
	return held, nil
}

// write replaces what the resource in dir says about itself.
func (st *Store) write(ctx context.Context, dir string, held meta) error {
	data, err := json.Marshal(held)
	if err != nil {
		return err
	}
	return st.fsys.Write(ctx, path.Join(dir, metaFile), data)
}

// dir is where the resource named name stands in s.
//
// User scope is refused: a scope with no workspace derives the path holding
// every workspace that user has, so a resource written there would stand where
// a workspace belongs.
func (st *Store) dir(s workspace.Scope, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	if s.Workspace == "" {
		return "", fmt.Errorf("scope names no workspace: %w", fs.ErrInvalid)
	}
	return s.Path(st.root, name)
}

// Resolve returns the resource the changed path declares: the scope it stands
// in and the name it has. It is dir read backwards, which is what turns a
// notification that something changed into the resource it is about.
//
// Only a resource's meta file declares one. Content changing is not a resource
// arriving — nothing reads it until the separate call that finalises it — and
// only what a resource says about itself can say that call has happened. Every
// other path is refused with fs.ErrNotExist, whether it is inside a resource,
// beside one, or under another root entirely.
func (st *Store) Resolve(changed string) (workspace.Scope, string, error) {
	s, inside, err := workspace.Locate(st.root, changed)
	if err != nil {
		return workspace.Scope{}, "", fmt.Errorf("resolve %q: %w", changed, err)
	}
	name, declares := path.Split(inside)
	name = strings.TrimSuffix(name, "/")
	if declares != metaFile || validName(name) != nil {
		return workspace.Scope{}, "", fmt.Errorf("resolve %q: it declares no resource: %w", changed, fs.ErrNotExist)
	}
	return s, name, nil
}

// resourceOf is what name and held say together. A resource that has not been
// completed carries no digest spelling, and the zero digest is what that means:
// nothing is named yet.
func resourceOf(name string, held meta) Resource {
	digest, err := cas.ParseDigest(held.Digest)
	if err != nil {
		digest = cas.Digest{}
	}
	return Resource{
		Name:        name,
		ContentType: held.ContentType,
		Size:        held.Size,
		Digest:      digest,
		Complete:    held.Complete,
	}
}
