package keyspace_test

import (
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/keyspace"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The collision rule, which is what every mount of this shape gets wrong
// separately if it states it separately: a key and a prefix share the name
// "abc", and the listing answers with the key whichever arrives first.
func TestAKeyIsListedOverAPrefixOfTheSameName(t *testing.T) {
	// The key is the older of the two, so nothing but the rule keeps it: a
	// prefix takes the newest ModTime beneath it, and this one is newer.
	key := vfs.FileInfo{Size: 11, Mode: 0o644, ModTime: time.Now().Add(-time.Hour)}
	deeper := vfs.FileInfo{Size: 7, Mode: 0o644, ModTime: time.Now()}

	for _, order := range []struct {
		name  string
		first string
		last  string
	}{
		{name: "the key first", first: "abc", last: "abc/state"},
		{name: "the prefix first", first: "abc/state", last: "abc"},
	} {
		t.Run(order.name, func(t *testing.T) {
			children := map[string]vfs.FileInfo{}
			keyspace.Fold(children, order.first, pick(order.first, key, deeper))
			keyspace.Fold(children, order.last, pick(order.last, key, deeper))

			listed, held := children["abc"]
			if !held {
				t.Fatalf("Fold listed %v, want a child named %q", children, "abc")
			}
			if listed.IsDir() || listed.Size != key.Size {
				t.Errorf("Fold listed %+v, want the key rather than the prefix", listed)
			}
		})
	}
}

// A prefix has no ModTime of its own, so it reports the newest of what is
// beneath it.
func TestAPrefixIsAsRecentAsTheNewestEntryUnderIt(t *testing.T) {
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	children := map[string]vfs.FileInfo{}
	keyspace.Fold(children, "logs/one", vfs.FileInfo{ModTime: newer})
	keyspace.Fold(children, "logs/two", vfs.FileInfo{ModTime: older})

	listed := children["logs"]
	if !listed.IsDir() {
		t.Fatalf("Fold listed %+v, want a prefix", listed)
	}
	if !listed.ModTime.Equal(newer) {
		t.Errorf("the prefix reports %v, want the newest entry's %v", listed.ModTime, newer)
	}
}

// The root is the one path that is a prefix and never an entry, which is what
// makes it the one path no verb reads, removes or moves.
func TestTheRootIsAPrefixAndNotAnEntry(t *testing.T) {
	for _, name := range []string{"", "/", "/./"} {
		if _, err := keyspace.Key(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Key(%q) = %v, want fs.ErrInvalid", name, err)
		}
		if under := keyspace.Prefix(name); under != "" {
			t.Errorf("Prefix(%q) = %q, want the empty prefix every entry is under", name, under)
		}
	}
}

// A caller is not made to clean paths a mount table would have cleaned.
func TestANameIsResolvedLexically(t *testing.T) {
	for name, want := range map[string]string{
		"sessions/abc":       "sessions/abc",
		"/sessions/abc":      "sessions/abc",
		"/sessions//abc":     "sessions/abc",
		"/sessions/./abc":    "sessions/abc",
		"/sessions/x/../abc": "sessions/abc",
	} {
		if got := keyspace.Relative(name); got != want {
			t.Errorf("Relative(%q) = %q, want %q", name, got, want)
		}
		at, err := keyspace.Key(name)
		if err != nil {
			t.Fatalf("Key(%q) = %v", name, err)
		}
		if at != want {
			t.Errorf("Key(%q) = %q, want %q", name, at, want)
		}
		if under := keyspace.Prefix(name); under != want+"/" {
			t.Errorf("Prefix(%q) = %q, want %q", name, under, want+"/")
		}
	}
}

// pick is which of the two entries the listing offers for rest, so a case can
// state the order the entries arrive in rather than repeating them.
func pick(rest string, key, deeper vfs.FileInfo) vfs.FileInfo {
	if rest == "abc" {
		return key
	}
	return deeper
}
