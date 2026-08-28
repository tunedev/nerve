package resource_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/resources"

func newStore(t *testing.T, plugin vfs.FileSystem) *resource.Store {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	signer, _ := newSigner(t, "a key nobody else has")
	store, err := resource.NewStore(noWholeContent{FileSystem: table}, root, signer)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// noWholeContent is the tree with the whole-content verbs refused on a
// resource's content. Every case runs over it, so a store that read or wrote a
// file it was asked to stream fails here rather than passing on a mount with
// enough memory to hide it.
type noWholeContent struct{ vfs.FileSystem }

func (n noWholeContent) Read(ctx context.Context, path string) ([]byte, error) {
	if strings.HasSuffix(path, "/content") {
		return nil, fmt.Errorf("read %q whole: content moves in pieces: %w", path, fs.ErrInvalid)
	}
	return n.FileSystem.Read(ctx, path)
}

func (n noWholeContent) Write(ctx context.Context, path string, data []byte) error {
	if strings.HasSuffix(path, "/content") {
		return fmt.Errorf("write %q whole: content moves in pieces: %w", path, fs.ErrInvalid)
	}
	return n.FileSystem.Write(ctx, path, data)
}

func mustCreate(t *testing.T, store *resource.Store, s workspace.Scope, name string) {
	t.Helper()
	if _, err := store.Create(t.Context(), s, name, "text/plain"); err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
}

func mustUpload(t *testing.T, store *resource.Store, s workspace.Scope, name, content string) {
	t.Helper()
	if _, err := store.Upload(t.Context(), s, name, strings.NewReader(content)); err != nil {
		t.Fatalf("Upload(%q): %v", name, err)
	}
}

func mustComplete(t *testing.T, store *resource.Store, s workspace.Scope, name string) resource.Resource {
	t.Helper()
	got, err := store.Complete(t.Context(), s, name)
	if err != nil {
		t.Fatalf("Complete(%q): %v", name, err)
	}
	return got
}

func mustDownload(t *testing.T, store *resource.Store, token string) string {
	t.Helper()

	content, err := store.Download(t.Context(), token)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer content.Close()

	read, err := io.ReadAll(content)
	if err != nil {
		t.Fatalf("reading the download: %v", err)
	}
	return string(read)
}

// The acceptance: content too large to hold arrives in pieces, and becomes
// readable only at the separate call that finalises it.
func TestLargeContentStreamsInAndFinalisesSeparately(t *testing.T) {
	store := newStore(t, memfs.New())
	large := bytes.Repeat([]byte("every byte of a file that will not fit in a call. "), 1<<16)

	mustCreate(t, store, here, "transcript.txt")
	written, err := store.Upload(t.Context(), here, "transcript.txt", bytes.NewReader(large))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if written != int64(len(large)) {
		t.Errorf("Upload wrote %d bytes, want %d", written, len(large))
	}

	got := mustComplete(t, store, here, "transcript.txt")
	if got.Size != int64(len(large)) {
		t.Errorf("Size = %d, want %d", got.Size, len(large))
	}
	if want := cas.Compute(large); got.Digest != want {
		t.Errorf("Digest = %s, want %s", got.Digest, want)
	}
	if !got.Complete {
		t.Error("a finalised resource reports itself incomplete")
	}

	granted, err := store.SignedDownload(t.Context(), here, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("SignedDownload: %v", err)
	}
	if read := mustDownload(t, store, granted.Token); read != string(large) {
		t.Errorf("downloaded %d bytes, want the %d uploaded", len(read), len(large))
	}
}

// Nothing hands out a link to a resource that is still arriving: the bytes on
// the mount are whatever the upload has managed so far, and a holder of a grant
// would read them as the whole file.
func TestResourceHasNoDownloadUntilItIsComplete(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "half of what is coming")

	if _, err := store.SignedDownload(t.Context(), here, "transcript.txt", time.Hour); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SignedDownload before Complete = %v, want %v", err, fs.ErrNotExist)
	}
}

// A store whose whole promise is a file larger than memory cannot keep it on a
// mount that has to hold one to store it.
func TestUploadNeedsAMountThatStreams(t *testing.T) {
	store := newStore(t, kvfs.New(kvfs.NewMemStore(), "resources"))

	mustCreate(t, store, here, "transcript.txt")

	_, err := store.Upload(t.Context(), here, "transcript.txt", strings.NewReader("bytes"))
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Upload over a mount with no streaming verbs = %v, want %v", err, fs.ErrInvalid)
	}
}

// A name is reserved by creating it. Letting a second create through would put
// one upload's bytes under another's declaration.
func TestCreateRefusesANameAlreadyTaken(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")

	if _, err := store.Create(t.Context(), here, "transcript.txt", "text/plain"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Create over an existing resource = %v, want %v", err, fs.ErrExist)
	}
}

func TestUploadBeforeCreateIsRefused(t *testing.T) {
	store := newStore(t, memfs.New())

	_, err := store.Upload(t.Context(), here, "transcript.txt", strings.NewReader("bytes"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Upload before Create = %v, want %v", err, fs.ErrNotExist)
	}
}

// Content that has been finalised is what its digest names. A second upload
// would leave the resource saying one thing and holding another.
func TestUploadAfterCompleteIsRefused(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "what was said")
	mustComplete(t, store, here, "transcript.txt")

	_, err := store.Upload(t.Context(), here, "transcript.txt", strings.NewReader("what was said instead"))
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("Upload after Complete = %v, want %v", err, fs.ErrExist)
	}
}

// Finalising is the call a client repeats when it did not hear the answer, so
// running it again reports the resource rather than failing on the work already
// done.
func TestCompleteRunAgainReturnsTheSameResource(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "what was said")

	first := mustComplete(t, store, here, "transcript.txt")
	if second := mustComplete(t, store, here, "transcript.txt"); second != first {
		t.Errorf("Complete run again = %+v, want %+v", second, first)
	}
}

func TestCompleteBeforeAnyUploadIsRefused(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")

	if _, err := store.Complete(t.Context(), here, "transcript.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Complete with nothing uploaded = %v, want %v", err, fs.ErrNotExist)
	}
}

// Delete is idempotent for the reason RemoveAll is: a delete that failed
// halfway is completed by running it again.
func TestDeleteRemovesTheResourceAndRunsAgain(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "what was said")
	mustComplete(t, store, here, "transcript.txt")

	if err := store.Delete(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(t.Context(), here, "transcript.txt"); err != nil {
		t.Errorf("Delete run again: %v", err)
	}
	if _, err := store.SignedDownload(t.Context(), here, "transcript.txt", time.Hour); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SignedDownload after Delete = %v, want %v", err, fs.ErrNotExist)
	}
}

// The grant names the scope, and the scope is what derives the path. Two users
// with a resource of one name are two resources.
func TestAGrantReadsOnlyTheScopeItNames(t *testing.T) {
	store := newStore(t, memfs.New())

	for scope, said := range map[workspace.Scope]string{here: "what ada said", other: "what grace said"} {
		mustCreate(t, store, scope, "transcript.txt")
		mustUpload(t, store, scope, "transcript.txt", said)
		mustComplete(t, store, scope, "transcript.txt")
	}

	granted, err := store.SignedDownload(t.Context(), here, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("SignedDownload: %v", err)
	}
	if read := mustDownload(t, store, granted.Token); read != "what ada said" {
		t.Errorf("Download = %q, want %q", read, "what ada said")
	}
}

// A grant outlives what it names, and says so when it is redeemed. Deletion is
// what a user asking for their data to be gone gets, not a token count.
func TestDownloadOfADeletedResourceDoesNotExist(t *testing.T) {
	store := newStore(t, memfs.New())

	mustCreate(t, store, here, "transcript.txt")
	mustUpload(t, store, here, "transcript.txt", "what was said")
	mustComplete(t, store, here, "transcript.txt")

	granted, err := store.SignedDownload(t.Context(), here, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("SignedDownload: %v", err)
	}
	if err := store.Delete(t.Context(), here, "transcript.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := store.Download(t.Context(), granted.Token); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Download of a deleted resource = %v, want %v", err, fs.ErrNotExist)
	}
}

// User scope is the subtree holding every workspace a user has, so a resource
// written there would stand where a workspace belongs. A name that is not flat
// names a place inside another resource.
func TestScopeWithNoWorkspaceAndAnEscapingNameAreRefused(t *testing.T) {
	store := newStore(t, memfs.New())

	if _, err := store.Create(t.Context(), workspace.Scope{User: "ada"}, "transcript.txt", "text/plain"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Create at user scope = %v, want %v", err, fs.ErrInvalid)
	}
	for _, name := range []string{"", "..", "../grace/transcript.txt", "nested/transcript.txt"} {
		if _, err := store.Create(t.Context(), here, name, "text/plain"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Create(%q) = %v, want %v", name, err, fs.ErrInvalid)
		}
	}
}
