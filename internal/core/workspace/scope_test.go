package workspace_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/core/workspace"
)

func TestPathPutsRestInsideTheWorkspace(t *testing.T) {
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	got, err := s.Path("/files", "drafts/one.md")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "/files/alice/cv/drafts/one.md"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestPathWithNoRestIsTheWorkspaceRoot(t *testing.T) {
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	got, err := s.Path("/kv", "")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "/kv/alice/cv"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// The same scope derives the same suffix under every area root, which is what
// lets a workspace be deleted from files, keys and queue entries alike.
func TestPathDerivesTheSameSuffixUnderEveryRoot(t *testing.T) {
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	for _, root := range []string{"/files", "/kv", "/queue/jobs", "objects"} {
		got, err := s.Path(root, "notes")
		if err != nil {
			t.Fatalf("Path(%q): %v", root, err)
		}
		if want := "/alice/cv/notes"; !strings.HasSuffix(got, want) {
			t.Errorf("Path(%q) = %q, want it to end in %q", root, got, want)
		}
	}
}

// A scope naming no workspace is user scope: the subtree holding every
// workspace that user has.
func TestPathWithNoWorkspaceIsTheUsersSubtree(t *testing.T) {
	s := workspace.Scope{User: "alice"}

	got, err := s.Path("/files", "")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "/files/alice"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// Containment is the point: a rest that climbs is clamped to the scope root
// rather than reaching the tree above it.
func TestPathClampsARestThatClimbsOut(t *testing.T) {
	s := workspace.Scope{User: "alice", Workspace: "cv"}

	for _, rest := range []string{"../bob/cv", "../../../etc/passwd", "/../..", "a/../../b"} {
		got, err := s.Path("/files", rest)
		if err != nil {
			t.Fatalf("Path(%q): %v", rest, err)
		}
		if want := "/files/alice/cv"; got != want && !strings.HasPrefix(got, want+"/") {
			t.Errorf("Path(%q) = %q, want it inside %q", rest, got, want)
		}
	}
}

// A segment carrying a separator or a dot name would place one user's scope
// inside another's, so it is refused rather than cleaned.
func TestPathRefusesASegmentThatIsNotAName(t *testing.T) {
	for _, s := range []workspace.Scope{
		{User: "", Workspace: "cv"},
		{User: "alice/bob", Workspace: "cv"},
		{User: "..", Workspace: "cv"},
		{User: ".", Workspace: "cv"},
		{User: "alice", Workspace: "cv/drafts"},
		{User: "alice", Workspace: ".."},
		{User: "alice", Workspace: "."},
	} {
		if _, err := s.Path("/files", "notes"); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Path for %+v: error = %v, want fs.ErrInvalid", s, err)
		}
	}
}

// The isolation property E5.4 proves end to end: the same workspace name under
// two users is two paths, and neither holds the other.
func TestPathsOfTwoUsersNeverNest(t *testing.T) {
	alice, err := (workspace.Scope{User: "alice", Workspace: "cv"}).Path("/files", "one.md")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	bob, err := (workspace.Scope{User: "bob", Workspace: "cv"}).Path("/files", "one.md")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	if alice == bob {
		t.Fatalf("both users derived %q", alice)
	}
	if strings.HasPrefix(alice, bob+"/") || strings.HasPrefix(bob, alice+"/") {
		t.Errorf("%q and %q nest", alice, bob)
	}
}

// The property that makes Locate the inverse rather than a second reader of the
// layout: whatever Path derived, Locate takes apart into what it was derived
// from.
func TestLocateIsTheInverseOfPath(t *testing.T) {
	for _, rest := range []string{"", "one.md", "drafts/one.md", "resources/t.txt/meta.json"} {
		s := workspace.Scope{User: "alice", Workspace: "cv"}
		name, err := s.Path("/files", rest)
		if err != nil {
			t.Fatalf("Path(%q): %v", rest, err)
		}

		got, inside, err := workspace.Locate("/files", name)
		if err != nil {
			t.Fatalf("Locate(%q): %v", name, err)
		}
		if got != s {
			t.Errorf("Locate(%q) scope = %+v, want %+v", name, got, s)
		}
		if inside != rest {
			t.Errorf("Locate(%q) inside = %q, want %q", name, inside, rest)
		}
	}
}

// A key comes off a queue without the leading slash a path carries, and it
// names the same place.
func TestLocateReadsAPathThatLostItsLeadingSlash(t *testing.T) {
	s, inside, err := workspace.Locate("/resources", "resources/alice/cv/t.txt/meta.json")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if want := (workspace.Scope{User: "alice", Workspace: "cv"}); s != want {
		t.Errorf("scope = %+v, want %+v", s, want)
	}
	if want := "t.txt/meta.json"; inside != want {
		t.Errorf("inside = %q, want %q", inside, want)
	}
}

// Nothing outside the area belongs to a scope in it, including a path whose
// spelling only starts the same way.
func TestLocateRefusesAPathOutsideTheRoot(t *testing.T) {
	for _, name := range []string{"/files", "/keys/alice/cv/one.md", "/filesystem/alice/cv/one.md", "/"} {
		if _, _, err := workspace.Locate("/files", name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Locate(%q) = %v, want fs.ErrNotExist", name, err)
		}
	}
}

// A path with only a user segment is the subtree holding every workspace that
// user has, which is not a place inside one.
func TestLocateRefusesAPathThatNamesNoWorkspace(t *testing.T) {
	if _, _, err := workspace.Locate("/files", "/files/alice"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Locate = %v, want fs.ErrNotExist", err)
	}
}
