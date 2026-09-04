package vectorfs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/vectorfs"
)

// corpora are the implementations of the port, run against one set of cases.
// That is the whole point of the port having two: what vectorfs is written
// against is a promise, and a promise only one implementation keeps is a struct
// with an interface in front of it.
//
// One keeps documents on the local disk and the other in an object store, which
// is the substitution the platform is built around: the laptop and the cloud,
// behind one set of verbs.
func corpora() map[string]func(*testing.T) vectorfs.DocStore {
	return map[string]func(*testing.T) vectorfs.DocStore{
		"LocalDocs":  newLocalDocs,
		"ObjectDocs": func(*testing.T) vectorfs.DocStore { return vectorfs.NewObjectDocs(newMemBucket()) },
	}
}

// newLocalDocs is a corpus in a directory of this test's own, given back when
// the test ends.
func newLocalDocs(t *testing.T) vectorfs.DocStore {
	t.Helper()
	docs, err := vectorfs.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocal = %v", err)
	}
	t.Cleanup(func() { _ = docs.Close() })
	return docs
}

// docCase is one promise of the port under the name it is reported by.
type docCase struct {
	name string
	run  func(t *testing.T, docs vectorfs.DocStore)
}

// docCases are the promises of the port. They are named here rather than inline
// so a run against a real object store is the same set, not a second set that
// drifts from this one.
func docCases() []docCase {
	return []docCase{
		{"Head describes a document without its body", headDescribesADocument},
		{"Head of an absent key does not exist", headOfAnAbsentDocument},
		{"Get of an absent key does not exist", getOfAnAbsentDocument},
		{"Put replaces what was there", putReplacesTheDocument},
		{"Put copies the caller's slice", putCopiesTheBody},
		{"Get hands back a copy", getCopiesTheBody},
		{"Put stores a document with nothing in it", putStoresAnEmptyDocument},
		{"Put moves the instant forward", putMovesTheInstantForward},
		{"A key is a name and not a path", keysAreNamesAndNotPaths},
		{"Delete takes the document out", deleteTakesTheDocumentOut},
		{"Delete of an absent key does not exist", deleteOfAnAbsentDocument},
		{"DeleteAll empties a prefix and leaves the rest", deleteAllEmptiesAPrefix},
		{"DeleteAll of a prefix nothing is under is not an error", deleteAllOfAnUnusedPrefix},
		{"Move carries the document across", moveCarriesTheDocument},
		{"Move replaces what it lands on", moveReplacesTheLanding},
		{"Move of an absent key does not exist", moveOfAnAbsentDocument},
		{"List answers what is under a prefix, however deep", listAnswersAPrefix},
		{"List of an empty prefix is the whole corpus", listOfTheWholeCorpus},
		{"List of a prefix nothing is under is empty", listOfAnUnusedPrefix},
		{"Chmod sets the bits without touching the body", chmodSetsTheBits},
		{"Chmod of an absent key does not exist", chmodOfAnAbsentDocument},
	}
}

func TestDocStorePort(t *testing.T) {
	for name, build := range corpora() {
		t.Run(name, func(t *testing.T) { runDocCases(t, build) })
	}
}

// runDocCases gives every case a corpus of its own, because each one starts from
// a store with nothing in it.
func runDocCases(t *testing.T, build func(*testing.T) vectorfs.DocStore) {
	t.Helper()
	for _, one := range docCases() {
		t.Run(one.name, func(t *testing.T) { one.run(t, build(t)) })
	}
}

func mustPutDoc(t *testing.T, docs vectorfs.DocStore, key, body string, mode fs.FileMode) {
	t.Helper()
	if err := docs.Put(context.Background(), key, []byte(body), mode); err != nil {
		t.Fatalf("Put(%q) = %v", key, err)
	}
}

func mustGetDoc(t *testing.T, docs vectorfs.DocStore, key string) []byte {
	t.Helper()
	body, err := docs.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get(%q) = %v", key, err)
	}
	return body
}

func keysOf(found []vectorfs.Entry) []string {
	keys := make([]string, len(found))
	for i, one := range found {
		keys[i] = one.Key
	}
	slices.Sort(keys)
	return keys
}

func headDescribesADocument(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off and jitter", 0o600)

	one, err := docs.Head(t.Context(), "notes/retries")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	if one.Key != "notes/retries" || one.Size != int64(len("back off and jitter")) || one.Mode.Perm() != 0o600 {
		t.Errorf("Head = %+v, want the key at size %d in mode 0600", one, len("back off and jitter"))
	}
	if one.Written.IsZero() {
		t.Error("Head reported no time, want the instant the store wrote it")
	}
}

func headOfAnAbsentDocument(t *testing.T, docs vectorfs.DocStore) {
	if _, err := docs.Head(t.Context(), "notes/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Head of an absent key = %v, want fs.ErrNotExist", err)
	}
}

func getOfAnAbsentDocument(t *testing.T, docs vectorfs.DocStore) {
	if _, err := docs.Get(t.Context(), "notes/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get of an absent key = %v, want fs.ErrNotExist", err)
	}
}

func putReplacesTheDocument(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o644)
	mustPutDoc(t, docs, "notes/retries", "back off and jitter", 0o600)

	if body := mustGetDoc(t, docs, "notes/retries"); string(body) != "back off and jitter" {
		t.Errorf("Get after a second Put = %q, want %q", body, "back off and jitter")
	}
	one, err := docs.Head(t.Context(), "notes/retries")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	if one.Mode.Perm() != 0o600 {
		t.Errorf("Head after a second Put = mode %v, want 0600", one.Mode.Perm())
	}
}

func putCopiesTheBody(t *testing.T, docs vectorfs.DocStore) {
	body := []byte("back off")
	if err := docs.Put(t.Context(), "notes/retries", body, 0o644); err != nil {
		t.Fatalf("Put = %v", err)
	}
	copy(body, "XXXX off")

	if stored := mustGetDoc(t, docs, "notes/retries"); string(stored) != "back off" {
		t.Errorf("Get after the caller changed its slice = %q, want %q", stored, "back off")
	}
}

func getCopiesTheBody(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o644)

	body := mustGetDoc(t, docs, "notes/retries")
	copy(body, "XXXX off")

	if stored := mustGetDoc(t, docs, "notes/retries"); string(stored) != "back off" {
		t.Errorf("Get after a reader changed what it was handed = %q, want %q", stored, "back off")
	}
}

// A document with nothing in it is a document. The mount above makes one every
// time a caller creates a path before writing to it, so a store that refused it
// would fail conformance one verb in.
func putStoresAnEmptyDocument(t *testing.T, docs vectorfs.DocStore) {
	if err := docs.Put(t.Context(), "notes/empty", nil, 0o644); err != nil {
		t.Fatalf("Put of nothing = %v", err)
	}

	one, err := docs.Head(t.Context(), "notes/empty")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	if one.Size != 0 {
		t.Errorf("Head of a document with nothing in it = size %d, want 0", one.Size)
	}
	if body := mustGetDoc(t, docs, "notes/empty"); len(body) != 0 {
		t.Errorf("Get of a document with nothing in it = %q, want nothing", body)
	}
}

// A store that stamps to the second reports the same instant for two writes a
// microsecond apart, so the case writes until the instant moves rather than
// waiting a whole one: the port asks only that a write moves time forward.
func putMovesTheInstantForward(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o644)
	first, err := docs.Head(t.Context(), "notes/retries")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mustPutDoc(t, docs, "notes/retries", "back off and jitter", 0o644)
		second, err := docs.Head(t.Context(), "notes/retries")
		if err != nil {
			t.Fatalf("Head = %v", err)
		}
		if second.Written.After(first.Written) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the instant after a second Put is still %v, want it later", first.Written)
		}
		time.Sleep(time.Millisecond)
	}
}

// A key is a flat name, so a corpus holds "notes" and "notes/one" at the same
// time. That is what the mount above claims with its object-store descriptor,
// and a directory tree cannot do it without help.
func keysAreNamesAndNotPaths(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes", "the index", 0o644)
	mustPutDoc(t, docs, "notes/one", "the first", 0o644)

	if body := mustGetDoc(t, docs, "notes"); string(body) != "the index" {
		t.Errorf("Get of the key beside a prefix of its name = %q, want %q", body, "the index")
	}
	if body := mustGetDoc(t, docs, "notes/one"); string(body) != "the first" {
		t.Errorf("Get under the prefix beside a key of its name = %q, want %q", body, "the first")
	}
}

func deleteTakesTheDocumentOut(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o644)

	if err := docs.Delete(t.Context(), "notes/retries"); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if _, err := docs.Get(t.Context(), "notes/retries"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get after Delete = %v, want fs.ErrNotExist", err)
	}
}

func deleteOfAnAbsentDocument(t *testing.T, docs vectorfs.DocStore) {
	if err := docs.Delete(t.Context(), "notes/absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Delete of an absent key = %v, want fs.ErrNotExist", err)
	}
}

func deleteAllEmptiesAPrefix(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/deep/one", "the first", 0o644)
	mustPutDoc(t, docs, "notes/deep/two", "the second", 0o644)
	mustPutDoc(t, docs, "notes/other", "elsewhere", 0o644)

	if err := docs.DeleteAll(t.Context(), "notes/deep/"); err != nil {
		t.Fatalf("DeleteAll = %v", err)
	}

	found, err := docs.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if got := keysOf(found); !slices.Equal(got, []string{"notes/other"}) {
		t.Errorf("List after DeleteAll = %v, want only %q", got, "notes/other")
	}
}

func deleteAllOfAnUnusedPrefix(t *testing.T, docs vectorfs.DocStore) {
	if err := docs.DeleteAll(t.Context(), "notes/nothing/"); err != nil {
		t.Errorf("DeleteAll of a prefix nothing is under = %v, want it to succeed", err)
	}
}

func moveCarriesTheDocument(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o600)

	if err := docs.Move(t.Context(), "notes/retries", "notes/deep/retries"); err != nil {
		t.Fatalf("Move = %v", err)
	}

	one, err := docs.Head(t.Context(), "notes/deep/retries")
	if err != nil {
		t.Fatalf("Head after Move = %v", err)
	}
	if one.Mode.Perm() != 0o600 {
		t.Errorf("Head after Move = mode %v, want the 0600 it moved with", one.Mode.Perm())
	}
	if body := mustGetDoc(t, docs, "notes/deep/retries"); string(body) != "back off" {
		t.Errorf("Get after Move = %q, want %q", body, "back off")
	}
	if _, err := docs.Get(t.Context(), "notes/retries"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get of the name it left = %v, want fs.ErrNotExist", err)
	}
}

func moveReplacesTheLanding(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o644)
	mustPutDoc(t, docs, "notes/pasta", "boil water", 0o644)

	if err := docs.Move(t.Context(), "notes/retries", "notes/pasta"); err != nil {
		t.Fatalf("Move = %v", err)
	}
	if body := mustGetDoc(t, docs, "notes/pasta"); string(body) != "back off" {
		t.Errorf("Get of the landing = %q, want %q", body, "back off")
	}
}

func moveOfAnAbsentDocument(t *testing.T, docs vectorfs.DocStore) {
	if err := docs.Move(t.Context(), "notes/absent", "notes/landing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Move of an absent key = %v, want fs.ErrNotExist", err)
	}
}

func listAnswersAPrefix(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/deep/one", "the first", 0o644)
	mustPutDoc(t, docs, "notes/two", "the second", 0o644)
	mustPutDoc(t, docs, "other/three", "elsewhere", 0o644)

	found, err := docs.List(t.Context(), "notes/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if got := keysOf(found); !slices.Equal(got, []string{"notes/deep/one", "notes/two"}) {
		t.Errorf("List(%q) = %v, want everything under it however deep", "notes/", got)
	}
	for _, one := range found {
		if one.Size == 0 {
			t.Errorf("List described %q as empty, want the size the store holds", one.Key)
		}
	}
}

func listOfTheWholeCorpus(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/one", "the first", 0o644)
	mustPutDoc(t, docs, "other/two", "elsewhere", 0o644)

	found, err := docs.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if got := keysOf(found); !slices.Equal(got, []string{"notes/one", "other/two"}) {
		t.Errorf("List of the empty prefix = %v, want the whole corpus", got)
	}
}

func listOfAnUnusedPrefix(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/one", "the first", 0o644)

	found, err := docs.List(t.Context(), "nothing/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if len(found) != 0 {
		t.Errorf("List of a prefix nothing is under = %v, want nothing", keysOf(found))
	}
}

func chmodSetsTheBits(t *testing.T, docs vectorfs.DocStore) {
	mustPutDoc(t, docs, "notes/retries", "back off", 0o644)

	if err := docs.Chmod(t.Context(), "notes/retries", 0o600); err != nil {
		t.Fatalf("Chmod = %v", err)
	}

	one, err := docs.Head(t.Context(), "notes/retries")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	if one.Mode.Perm() != 0o600 {
		t.Errorf("Head after Chmod = mode %v, want 0600", one.Mode.Perm())
	}
	if body := mustGetDoc(t, docs, "notes/retries"); string(body) != "back off" {
		t.Errorf("Get after Chmod = %q, want the body untouched", body)
	}
}

func chmodOfAnAbsentDocument(t *testing.T, docs vectorfs.DocStore) {
	if err := docs.Chmod(t.Context(), "notes/absent", 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Chmod of an absent key = %v, want fs.ErrNotExist", err)
	}
}

// A corpus on a disk files a document under a name no directory will take, so
// what is on disk is the key plus one suffix and nothing else. The case is here
// rather than in the port's set because it is the local store's own answer to a
// promise the port makes for both.
func TestLocalDocsFilesAKeyWhereADirectoryCannotReach(t *testing.T) {
	root := t.TempDir()
	docs, err := vectorfs.OpenLocal(root)
	if err != nil {
		t.Fatalf("OpenLocal = %v", err)
	}
	t.Cleanup(func() { _ = docs.Close() })

	mustPutDoc(t, docs, "notes", "the index", 0o644)
	mustPutDoc(t, docs, "notes/one", "the first", 0o644)

	var names []string
	if err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			names = append(names, name)
		}
		return err
	}); err != nil {
		t.Fatalf("walking the corpus = %v", err)
	}
	slices.Sort(names)

	want := []string{"notes.doc", "notes/one.doc"}
	if !slices.Equal(names, want) {
		t.Errorf("the corpus on disk holds %v, want %v", names, want)
	}
}

// DeleteAll leaves no empty directories behind, so a corpus that has been
// emptied looks like one that was never written to.
func TestLocalDocsLeavesNoEmptyDirectoriesBehind(t *testing.T) {
	root := t.TempDir()
	docs, err := vectorfs.OpenLocal(root)
	if err != nil {
		t.Fatalf("OpenLocal = %v", err)
	}
	t.Cleanup(func() { _ = docs.Close() })

	mustPutDoc(t, docs, "notes/deep/one", "the first", 0o644)
	if err := docs.DeleteAll(t.Context(), ""); err != nil {
		t.Fatalf("DeleteAll = %v", err)
	}

	var left []string
	if err := fs.WalkDir(os.DirFS(root), ".", func(name string, _ fs.DirEntry, err error) error {
		if err == nil && name != "." {
			left = append(left, name)
		}
		return err
	}); err != nil {
		t.Fatalf("walking the corpus = %v", err)
	}
	if len(left) != 0 {
		t.Errorf("the emptied corpus still holds %v, want nothing", left)
	}
}
