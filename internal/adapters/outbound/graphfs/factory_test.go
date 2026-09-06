package graphfs_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/graphfs"
)

func TestFactoryBuildsAGraphFromItsSettings(t *testing.T) {
	build := graphfs.Factory(statements{}, newMemStore(), newMemStore())

	mount, err := build(map[string]string{"name": "notes", "edges": "3"})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	body, err := mount.Read(t.Context(), "/config")
	if err != nil {
		t.Fatalf("Read(/config) = %v", err)
	}
	if got, want := string(body), "graph notes\nedges 3\n"; got != want {
		t.Errorf("Read(/config) = %q, want %q", got, want)
	}
}

func TestFactoryDefaultsTheBoundAMountDoesNotName(t *testing.T) {
	build := graphfs.Factory(statements{}, newMemStore(), newMemStore())

	mount, err := build(map[string]string{"name": "notes"})
	if err != nil {
		t.Fatalf("Factory = %v", err)
	}

	body, _ := mount.Read(t.Context(), "/config")
	if !strings.Contains(string(body), "edges 20") {
		t.Errorf("Read(/config) = %q, want the default bound", body)
	}
}

// A build whose chain reads no entities, a build with nowhere to keep a graph
// and a build with nowhere to keep a snapshot of one are all refused where an
// operator is still watching, rather than on the first text or the first
// snapshot.
func TestAMountIsRefusedWithNothingBehindIt(t *testing.T) {
	settings := map[string]string{"name": "notes"}

	if _, err := graphfs.Factory(nil, newMemStore(), newMemStore())(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a chain that reads no entities built a mount: %v", err)
	}
	if _, err := graphfs.Factory(statements{}, nil, newMemStore())(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a build with no store built a mount: %v", err)
	}
	if _, err := graphfs.Factory(statements{}, newMemStore(), nil)(settings); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("a build with nowhere to keep a snapshot built a mount: %v", err)
	}
}

// A misspelled setting is a boot failure rather than a line quietly ignored.
func TestASettingThisKindDoesNotTakeIsRefused(t *testing.T) {
	build := graphfs.Factory(statements{}, newMemStore(), newMemStore())

	_, err := build(map[string]string{"name": "notes", "hops": "2"})
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "hops") {
		t.Errorf("Factory = %v, want the setting it does not take named", err)
	}
}

func TestAGraphWithNoNameIsRefused(t *testing.T) {
	build := graphfs.Factory(statements{}, newMemStore(), newMemStore())

	if _, err := build(map[string]string{}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory = %v, want fs.ErrInvalid", err)
	}
}

func TestABoundThatIsNotACountIsRefused(t *testing.T) {
	build := graphfs.Factory(statements{}, newMemStore(), newMemStore())

	for _, spelled := range []string{"many", "0", "-1"} {
		if _, err := build(map[string]string{"name": "notes", "edges": spelled}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("edges = %q built a mount: %v", spelled, err)
		}
	}
}

// A store that cannot be read is a mount that does not come up. A graph that
// booted empty because a bucket was briefly unreachable would overwrite the
// real one on its first write.
func TestAStoreThatCannotBeReadRefusesTheMount(t *testing.T) {
	build := graphfs.Factory(statements{}, unreadable{}, newMemStore())

	if _, err := build(map[string]string{"name": "notes"}); err == nil {
		t.Fatal("Factory = nil, want the store's refusal")
	}
}

// unreadable is a store whose blobs cannot be fetched, which is a bucket that
// is not answering rather than a graph that is not there yet.
type unreadable struct{}

func (unreadable) Load(context.Context, string) ([]byte, error) {
	return nil, errors.New("the bucket is not answering")
}

func (unreadable) Save(context.Context, string, []byte) error { return nil }
