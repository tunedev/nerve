package treecopy_test

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/treecopy"
	"github.com/tunedev/nerve/internal/core/cas"
	"github.com/tunedev/nerve/internal/core/version"
	"github.com/tunedev/nerve/internal/core/vfs"
)

const (
	root  = "/work"
	state = "/versions/base"
)

// A copy over a mounted tree answers the port a versioner holds, which is the
// only reason it can be versioned at all.
var _ version.WorkingCopy = treecopy.New(memfs.New(), root, state)

// stands is a version the copy can be put at: a digest of something, rather
// than a string that was never one.
var stands = cas.Compute([]byte("a version"))

func newCopy(t *testing.T) (vfs.FileSystem, *treecopy.Copy) {
	t.Helper()
	fsys := memfs.New()
	return fsys, treecopy.New(fsys, root, state)
}

func walked(t *testing.T, copy *treecopy.Copy) []string {
	t.Helper()

	var found []string
	err := copy.Walk(context.Background(), func(name string, _ []byte) error {
		found = append(found, name)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	slices.Sort(found)
	return found
}

// A copy that has never been put anywhere stands nowhere. The zero ID is what
// that is, and a versioner comparing against it is comparing against the empty
// version rather than handling an error.
func TestACopyThatHasNeverBeenPutAnywhereStandsNowhere(t *testing.T) {
	_, copy := newCopy(t)

	stood, err := copy.Base(context.Background())
	if err != nil {
		t.Fatalf("Base of a fresh copy: %v", err)
	}
	if !stood.IsZero() {
		t.Errorf("a fresh copy stands at %s, want the zero ID", stood)
	}
}

func TestWhereACopyStandsSurvivesBeingRead(t *testing.T) {
	ctx := context.Background()
	_, copy := newCopy(t)

	if err := copy.SetBase(ctx, stands); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	stood, err := copy.Base(ctx)
	if err != nil {
		t.Fatalf("Base: %v", err)
	}
	if stood != stands {
		t.Errorf("the copy stands at %s, want the version it was put at", stood)
	}
}

// Where a copy stands is kept beside it and not in it. A file inside the root
// would be content the copy holds: every version would record it and every diff
// would report it changing on each commit.
func TestWhereACopyStandsIsNotContentOfIt(t *testing.T) {
	ctx := context.Background()
	fsys, copy := newCopy(t)

	if err := copy.Write(ctx, "a.md", []byte("the only content")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := copy.SetBase(ctx, stands); err != nil {
		t.Fatalf("SetBase: %v", err)
	}

	if got := walked(t, copy); !slices.Equal(got, []string{"a.md"}) {
		t.Errorf("the copy holds %v, want only what was written to it", got)
	}
	if _, err := fsys.Stat(ctx, state); err != nil {
		t.Errorf("where the copy stands was not kept at %q: %v", state, err)
	}
}

// Writing makes the directories on the way, because a version records content
// at paths and says nothing about the directories they are under.
func TestWriteMakesTheDirectoriesOnTheWay(t *testing.T) {
	ctx := context.Background()
	_, copy := newCopy(t)

	if err := copy.Write(ctx, "notes/deep/one.md", []byte("a note")); err != nil {
		t.Fatalf("write into a directory that is not there: %v", err)
	}
	if got := walked(t, copy); !slices.Equal(got, []string{"notes/deep/one.md"}) {
		t.Errorf("the copy holds %v, want the path that was written", got)
	}
}

// A copy whose root is not there yet holds no files rather than failing: the
// tree of a version that has never been committed is empty.
func TestACopyWhoseRootIsNotThereIsEmpty(t *testing.T) {
	_, copy := newCopy(t)

	if got := walked(t, copy); len(got) != 0 {
		t.Errorf("a copy that was never written to holds %v, want nothing", got)
	}
}

// Standing nowhere is a thing a copy can be put back to, and it survives being
// written down: the zero ID spells itself as nothing, and nothing reads back as
// the zero ID rather than as a version that cannot be found.
func TestACopyCanBePutBackToStandingNowhere(t *testing.T) {
	ctx := context.Background()
	_, copy := newCopy(t)

	if err := copy.SetBase(ctx, version.ID{}); err != nil {
		t.Fatalf("SetBase(zero): %v", err)
	}
	stood, err := copy.Base(ctx)
	if err != nil {
		t.Fatalf("Base: %v", err)
	}
	if !stood.IsZero() {
		t.Errorf("the copy stands at %s, want the zero ID", stood)
	}
}

// A state file holding something that was never a version fails here, where the
// bad value is, rather than in the commit or the sweep that would have taken it
// for a version.
func TestBaseRefusesAStateThatIsNotAVersion(t *testing.T) {
	ctx := context.Background()
	fsys, copy := newCopy(t)

	if err := fsys.Mkdir(ctx, path.Dir(state)); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fsys.Write(ctx, state, []byte("not a digest")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := copy.Base(ctx); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Base over a state that is not a version = %v, want fs.ErrInvalid", err)
	}
}
