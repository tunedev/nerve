package hellofs_test

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/hellofs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

// The acceptance for a utility plugin is that it mounts and answers stat, and
// stat is the verb that says an entry is there without reading it.
func TestStatDescribesTheGreeting(t *testing.T) {
	fsys := hellofs.New("hello")

	info, err := fsys.Stat(t.Context(), path.Join("/", hellofs.Greeting))
	if err != nil {
		t.Fatalf("Stat(%q) = %v", hellofs.Greeting, err)
	}
	if info.Name != hellofs.Greeting {
		t.Errorf("Stat().Name = %q, want %q", info.Name, hellofs.Greeting)
	}
	if info.Size != int64(len("hello\n")) {
		t.Errorf("Stat().Size = %d, want %d", info.Size, len("hello\n"))
	}
	if info.IsDir() {
		t.Error("Stat().IsDir() = true, want a file")
	}
}

func TestReadHandsBackTheMessage(t *testing.T) {
	fsys := hellofs.New("greetings from the forge")

	content, err := fsys.Read(t.Context(), "/greeting")
	if err != nil {
		t.Fatalf("Read(/greeting) = %v", err)
	}
	if string(content) != "greetings from the forge\n" {
		t.Errorf("Read(/greeting) = %q, want the message and a newline", content)
	}
}

// A caller that keeps what a read handed back must not be able to change what
// the next caller reads.
func TestReadCopiesWhatItHandsBack(t *testing.T) {
	fsys := hellofs.New("hello")

	first, err := fsys.Read(t.Context(), "/greeting")
	if err != nil {
		t.Fatalf("Read(/greeting) = %v", err)
	}
	first[0] = 'j'

	second, err := fsys.Read(t.Context(), "/greeting")
	if err != nil {
		t.Fatalf("the second Read(/greeting) = %v", err)
	}
	if string(second) != "hello\n" {
		t.Errorf("the second Read = %q after the first was written over, want %q", second, "hello\n")
	}
}

func TestReadDirListsTheOneEntry(t *testing.T) {
	entries, err := hellofs.New("hello").ReadDir(t.Context(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) = %v", err)
	}

	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	if !slices.Equal(listed, []string{hellofs.Greeting}) {
		t.Errorf("ReadDir(/) = %v, want [%s]", listed, hellofs.Greeting)
	}
}

func TestCapabilitiesAreReadOnlyEverywhere(t *testing.T) {
	fsys := hellofs.New("hello")

	for _, name := range []string{"/", "/greeting", "/absent"} {
		if caps := fsys.Capabilities(name); caps != (vfs.Caps{ReadOnly: true}) {
			t.Errorf("Capabilities(%q) = %+v, want read-only", name, caps)
		}
	}
}

func TestAMountWithNoMessageStillGreets(t *testing.T) {
	fsys, err := hellofs.Factory(nil)
	if err != nil {
		t.Fatalf("Factory(nil) = %v", err)
	}

	content, err := fsys.Read(t.Context(), "/greeting")
	if err != nil {
		t.Fatalf("Read(/greeting) = %v", err)
	}
	if string(content) != hellofs.DefaultMessage+"\n" {
		t.Errorf("Read(/greeting) = %q, want %q", content, hellofs.DefaultMessage)
	}
}

// A setting this kind cannot honour is one meant for another kind, and boot is
// the last place an operator can still act on it.
func TestFactoryRefusesASettingItCannotHonour(t *testing.T) {
	if _, err := hellofs.Factory(map[string]string{"root": "/srv"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(root) = %v, want fs.ErrInvalid", err)
	}
}
