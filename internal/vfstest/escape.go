package vfstest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withheld is what an entry outside the mount holds, so a case that reads one
// says so rather than reporting a bare success.
const withheld = "not yours"

// escapeCases hold a kind whose storage sits inside something larger to the
// boundary it draws. A mount table cleans ".." out of a path before any plugin
// sees it, so these cases call the plugin directly: the containment has to be
// the plugin's own, because the next caller may be a test, a replay or another
// mount. A symlink is not cleaned at all — the kernel follows it at the syscall,
// long after any name could have been resolved — so nothing above the plugin
// can refuse one on its behalf.
//
// A kind that stores its own tree has no outside to escape to and skips these.
func escapeCases() []testCase {
	return []testCase{
		{name: "A path out of the root reads nothing outside it", run: climbingReadsNothing},
		{name: "A path out of the root writes nothing outside it", when: writable, run: climbingWritesNothing},
		{name: "A planted link out of the root is not read through", run: plantedLinkIsNotRead},
		{name: "A planted link out of the root is not written through", when: writable, run: plantedLinkIsNotWritten},
		{name: "Emptying the root deletes a planted link and not its target", when: writable, run: emptyingKeepsTheTarget},
	}
}

// outside plants an entry the mount must never reach and returns where it is on
// the host together with the path a caller inside the mount would spell to
// climb to it. The skip is the answer for a kind with no host: an in-memory
// tree has nowhere to plant one.
func outside(t *testing.T, w world) (host, climb string) {
	t.Helper()
	if w.host == "" {
		t.Skip("the plugin is not backed by a host directory")
	}

	host = filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(host, []byte(withheld), 0o600); err != nil {
		t.Fatalf("planting the entry outside the mount = %v", err)
	}
	steps, err := filepath.Rel(w.host, host)
	if err != nil {
		t.Fatalf("Rel(%q, %q) = %v", w.host, host, err)
	}
	return host, filepath.ToSlash(steps)
}

// spellings are the ways a caller writes a path that climbs out of the mount:
// as a path from the root, and rooted at it. Neither is cleaned first, because
// the plugin is what has to refuse them.
func spellings(w world, climb string) []string {
	return []string{climb, strings.TrimSuffix(w.root, "/") + "/" + climb}
}

func climbingReadsNothing(t *testing.T, w world) {
	host, climb := outside(t, w)

	for _, name := range spellings(w, climb) {
		content, err := w.fsys.Read(t.Context(), name)
		if err == nil {
			t.Errorf("Read(%q) = %q, want the mount to refuse a path out of it", name, content)
		}
	}

	if _, err := os.Stat(host); err != nil {
		t.Errorf("the entry outside the mount = %v, want it untouched", err)
	}
}

func climbingWritesNothing(t *testing.T, w world) {
	host, climb := outside(t, w)

	for _, name := range spellings(w, climb) {
		if err := w.fsys.Write(t.Context(), name, []byte("mine now")); err == nil {
			t.Errorf("Write(%q) = nil, want the mount to refuse a path out of it", name)
		}
	}

	mustHoldSecret(t, host)
}

// plant writes two links into the mount from the host side, which is how they
// arrive in the field: another process, another user or an extracted archive
// puts them there, and for a kind whose whole purpose is passthrough onto a
// host directory that is the normal condition rather than an exotic one. One
// link names the entry outside the mount, the other the directory holding it.
func plant(t *testing.T, w world, host string) {
	t.Helper()
	if err := os.Symlink(host, filepath.Join(w.host, "straight-out")); err != nil {
		t.Fatalf("planting the link = %v", err)
	}
	if err := os.Symlink(filepath.Dir(host), filepath.Join(w.host, "escape")); err != nil {
		t.Fatalf("planting the link = %v", err)
	}
}

func plantedLinkIsNotRead(t *testing.T, w world) {
	host, _ := outside(t, w)
	plant(t, w, host)

	for _, name := range []string{"straight-out", "escape/secret"} {
		content, err := w.fsys.Read(t.Context(), w.at(name))
		if err == nil {
			t.Errorf("Read(%q) = %q, want the mount to refuse a link out of it", w.at(name), content)
		}
	}
}

func plantedLinkIsNotWritten(t *testing.T, w world) {
	host, _ := outside(t, w)
	plant(t, w, host)

	for _, name := range []string{"straight-out", "escape/planted"} {
		if err := w.fsys.Write(t.Context(), w.at(name), []byte("mine now")); err == nil {
			t.Errorf("Write(%q) = nil, want the mount to refuse a link out of it", w.at(name))
		}
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(host), "planted")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the entry outside the mount = %v, want it never written", err)
	}
	mustHoldSecret(t, host)
}

// Emptying the root is the one verb where following a planted link would
// destroy another workspace rather than read one.
func emptyingKeepsTheTarget(t *testing.T, w world) {
	host, _ := outside(t, w)
	plant(t, w, host)

	if err := w.fsys.RemoveAll(t.Context(), w.root); err != nil {
		t.Fatalf("RemoveAll(%q) = %v", w.root, err)
	}

	if _, err := os.Lstat(filepath.Join(w.host, "straight-out")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link inside the mount = %v, want it deleted", err)
	}
	mustHoldSecret(t, host)
}

func mustHoldSecret(t *testing.T, host string) {
	t.Helper()
	content, err := os.ReadFile(host)
	if err != nil {
		t.Fatalf("the entry outside the mount = %v, want it untouched", err)
	}
	if string(content) != withheld {
		t.Errorf("the entry outside the mount = %q, want %q", content, withheld)
	}
}
