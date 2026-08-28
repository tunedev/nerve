package kv_test

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/kvfs"
	"github.com/tunedev/nerve/internal/adapters/outbound/memfs"
	"github.com/tunedev/nerve/internal/core/kv"
	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

const root = "/kv"

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	there = workspace.Scope{User: "ada", Workspace: "notes"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}
)

// plugins are the two shapes a scratch key space can sit on: a tree, where a
// namespace is a directory something has to make, and a keyspace, where it is a
// prefix that exists because keys are under it. Every verb is asked on both,
// because which one a mount is holds no interest above the port.
var plugins = map[string]func() vfs.FileSystem{
	"tree":     func() vfs.FileSystem { return memfs.New() },
	"keyspace": func() vfs.FileSystem { return kvfs.New(kvfs.NewMemStore(), "scratch") },
}

func newSpace(t *testing.T, plugin func() vfs.FileSystem) (*kv.Space, vfs.FileSystem) {
	t.Helper()

	table := vfs.NewMountTable()
	if err := table.Mount(root, plugin()); err != nil {
		t.Fatalf("Mount(%q): %v", root, err)
	}
	space, err := kv.NewSpace(table, root)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	return space, table
}

// onBoth runs one case against every plugin, so a promise kept over a tree and
// broken over a keyspace fails rather than passing on the half that was tried.
func onBoth(t *testing.T, run func(t *testing.T, space *kv.Space, fsys vfs.FileSystem)) {
	t.Helper()
	for name, plugin := range plugins {
		t.Run(name, func(t *testing.T) {
			space, fsys := newSpace(t, plugin)
			run(t, space, fsys)
		})
	}
}

func mustPut(t *testing.T, space *kv.Space, s workspace.Scope, namespace, key, value string) {
	t.Helper()
	if err := space.Put(t.Context(), s, namespace, key, []byte(value)); err != nil {
		t.Fatalf("Put(%q/%q): %v", namespace, key, err)
	}
}

func mustGet(t *testing.T, space *kv.Space, s workspace.Scope, namespace, key string) string {
	t.Helper()
	value, err := space.Get(t.Context(), s, namespace, key)
	if err != nil {
		t.Fatalf("Get(%q/%q): %v", namespace, key, err)
	}
	return string(value)
}

func TestPutAndGetRoundTrip(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		mustPut(t, space, here, "run", "cursor", "page 3")

		if got := mustGet(t, space, here, "run", "cursor"); got != "page 3" {
			t.Fatalf("Get = %q, want %q", got, "page 3")
		}
	})
}

// A value is fetched and replaced whole. There is no update verb because Put is
// one: the way to change what a key holds is to put it again.
func TestPutReplacesTheValue(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		mustPut(t, space, here, "run", "cursor", "page 3")
		mustPut(t, space, here, "run", "cursor", "page 4")

		if got := mustGet(t, space, here, "run", "cursor"); got != "page 4" {
			t.Fatalf("Get = %q, want %q", got, "page 4")
		}
	})
}

// An empty value is a value. Nothing there and nothing in it are told apart by
// the error, not by the bytes.
func TestPutAndGetEmptyValue(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		mustPut(t, space, here, "run", "cursor", "")

		value, err := space.Get(t.Context(), here, "run", "cursor")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(value) != 0 {
			t.Fatalf("Get = %q, want no bytes", value)
		}
	})
}

func TestGetWhatWasNeverPut(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		if _, err := space.Get(t.Context(), here, "run", "cursor"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Get = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestDeleteRemovesTheValue(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		mustPut(t, space, here, "run", "cursor", "page 3")

		if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := space.Get(t.Context(), here, "run", "cursor"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Get after Delete = %v, want fs.ErrNotExist", err)
		}
	})
}

// A delete that failed halfway is completed by running it again, which is only
// true if the second run is not an error.
func TestDeleteWhatIsNotThere(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
			t.Fatalf("Delete of nothing: %v", err)
		}

		mustPut(t, space, here, "run", "cursor", "page 3")
		if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
			t.Fatalf("second Delete: %v", err)
		}
	})
}

// The namespace is what the space is for: one key name means one value per
// namespace, so state an agent puts aside under its own name is never state
// another one reads.
func TestNamespacesHoldTheirOwnKeys(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		mustPut(t, space, here, "run", "cursor", "page 3")
		mustPut(t, space, here, "review", "cursor", "line 12")

		if got := mustGet(t, space, here, "run", "cursor"); got != "page 3" {
			t.Fatalf("Get run/cursor = %q, want %q", got, "page 3")
		}
		if got := mustGet(t, space, here, "review", "cursor"); got != "line 12" {
			t.Fatalf("Get review/cursor = %q, want %q", got, "line 12")
		}

		if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got := mustGet(t, space, here, "review", "cursor"); got != "line 12" {
			t.Fatalf("Get review/cursor after deleting run/cursor = %q, want %q", got, "line 12")
		}
	})
}

// Scratch state is inside the workspace that wrote it. Two workspaces of one
// user, and two users, are the two ways that has to hold.
func TestScopesHoldTheirOwnKeys(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		mustPut(t, space, here, "run", "cursor", "hunt")
		mustPut(t, space, there, "run", "cursor", "notes")
		mustPut(t, space, other, "run", "cursor", "grace")

		for _, one := range []struct {
			scope workspace.Scope
			want  string
		}{{here, "hunt"}, {there, "notes"}, {other, "grace"}} {
			if got := mustGet(t, space, one.scope, "run", "cursor"); got != one.want {
				t.Fatalf("Get in %+v = %q, want %q", one.scope, got, one.want)
			}
		}

		if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got := mustGet(t, space, other, "run", "cursor"); got != "grace" {
			t.Fatalf("Get in %+v after deleting another user's key = %q, want %q", other, got, "grace")
		}
	})
}

// Deleting a key deletes the key. Over a keyspace a key and a prefix can share
// a name, so a delete that reached under the key would take out whatever else
// on the mount is stored beneath it.
func TestDeleteLeavesWhatIsUnderTheKey(t *testing.T) {
	space, fsys := newSpace(t, plugins["keyspace"])
	mustPut(t, space, here, "run", "cursor", "page 3")

	beneath, err := here.Path(root, path.Join("run", "cursor", "deeper"))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := fsys.Write(t.Context(), beneath, []byte("kept")); err != nil {
		t.Fatalf("Write(%q): %v", beneath, err)
	}

	if err := space.Delete(t.Context(), here, "run", "cursor"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := fsys.Read(t.Context(), beneath)
	if err != nil {
		t.Fatalf("Read(%q) after Delete: %v", beneath, err)
	}
	if !bytes.Equal(got, []byte("kept")) {
		t.Fatalf("Read(%q) = %q, want %q", beneath, got, "kept")
	}
}

// A namespace and a key are one flat segment each, for the reason a scope's
// segments are: a name carrying a separator names a place inside another one,
// and a name that resolves away lands on a key nobody asked for.
func TestRefusesNamesThatAreNotOneSegment(t *testing.T) {
	cases := map[string]struct{ namespace, key string }{
		"empty namespace":        {"", "cursor"},
		"empty key":              {"run", ""},
		"namespace separator":    {"run/deeper", "cursor"},
		"key separator":          {"run", "deeper/cursor"},
		"namespace climbs":       {"..", "cursor"},
		"key climbs":             {"run", ".."},
		"namespace is the dot":   {".", "cursor"},
		"key is the dot":         {"run", "."},
		"key climbs in the tail": {"run", "keys/.."},
	}

	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		for name, one := range cases {
			t.Run(name, func(t *testing.T) {
				if err := space.Put(t.Context(), here, one.namespace, one.key, []byte("x")); !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("Put = %v, want fs.ErrInvalid", err)
				}
				if _, err := space.Get(t.Context(), here, one.namespace, one.key); !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("Get = %v, want fs.ErrInvalid", err)
				}
				if err := space.Delete(t.Context(), here, one.namespace, one.key); !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("Delete = %v, want fs.ErrInvalid", err)
				}
			})
		}
	})
}

// A scope naming no workspace is the subtree holding every workspace the user
// has. A namespace written there would stand where a workspace belongs and be
// listed as one.
func TestRefusesUserScope(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		user := workspace.Scope{User: "ada"}

		if err := space.Put(t.Context(), user, "run", "cursor", []byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Put = %v, want fs.ErrInvalid", err)
		}
		if _, err := space.Get(t.Context(), user, "run", "cursor"); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Get = %v, want fs.ErrInvalid", err)
		}
		if err := space.Delete(t.Context(), user, "run", "cursor"); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Delete = %v, want fs.ErrInvalid", err)
		}
	})
}

// A scope that cannot derive a path is refused by the scope, and the verbs
// report what it said rather than writing somewhere else.
func TestRefusesScopeThatIsNotFlat(t *testing.T) {
	onBoth(t, func(t *testing.T, space *kv.Space, _ vfs.FileSystem) {
		if err := space.Put(t.Context(), workspace.Scope{User: "ada/grace", Workspace: "hunt"}, "run", "cursor", []byte("x")); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Put = %v, want fs.ErrInvalid", err)
		}
	})
}

// The root is where a mount was put, so it is the mount table's rule for a
// prefix. A space over a root nothing can be mounted at is a startup failure.
func TestNewSpaceRefusesARootThatIsNotAPrefix(t *testing.T) {
	for name, bad := range map[string]string{
		"empty":    "",
		"relative": "kv",
		"unclean":  "/kv/",
		"climbing": "/kv/../kv",
		"dot":      "/kv/.",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := kv.NewSpace(vfs.NewMountTable(), bad); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("NewSpace(%q) = %v, want fs.ErrInvalid", bad, err)
			}
		})
	}
}
